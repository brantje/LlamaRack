package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Service) BootstrapRequired(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func (s *Service) Bootstrap(ctx context.Context, username, password string) (User, error) {
	username, err := validateCredentials(username, password)
	if err != nil {
		return User{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return User{}, err
	}
	if count != 0 {
		return User{}, errors.New("bootstrap already completed")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	now := time.Now().Unix()
	result, err := tx.ExecContext(ctx, "INSERT INTO users(username,password_hash,created_at) VALUES(?,?,?)", username, hash, now)
	if err != nil {
		return User{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, Enabled: true, CreatedAt: now}, nil
}

func (s *Service) CreateUser(ctx context.Context, username, password string) (User, error) {
	username, err := validateCredentials(username, password)
	if err != nil {
		return User{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	now := time.Now().Unix()
	result, err := s.db.ExecContext(ctx, "INSERT INTO users(username,password_hash,created_at) VALUES(?,?,?)", username, hash, now)
	if err != nil {
		return User{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, Enabled: true, CreatedAt: now}, nil
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,username,enabled,created_at,last_login_at FROM users ORDER BY username COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		user, err := scanUser(rows.Scan)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Service) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT id,username,enabled,created_at,last_login_at FROM users WHERE id=?", id).Scan)
}

func (s *Service) SetUserEnabled(ctx context.Context, id int64, enabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM users WHERE id=?", id).Scan(&current); err != nil {
		return err
	}
	if !enabled && current != 0 {
		var enabledCount int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE enabled=1").Scan(&enabledCount); err != nil {
			return err
		}
		if enabledCount <= 1 {
			return ErrLastEnabledUser
		}
	}
	value := 0
	if enabled {
		value = 1
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET enabled=? WHERE id=?", value, id); err != nil {
		return err
	}
	if !enabled {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) DeleteUser(ctx context.Context, actorID, id int64) error {
	if actorID == id {
		return ErrSelfDelete
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var enabled int
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM users WHERE id=?", id).Scan(&enabled); err != nil {
		return err
	}
	if enabled != 0 {
		var enabledCount int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE enabled=1").Scan(&enabledCount); err != nil {
			return err
		}
		if enabledCount <= 1 {
			return ErrLastEnabledUser
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) ResetPassword(ctx context.Context, userID int64, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=?", hash, userID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword, keepSessionID string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	var currentHash string
	if err := s.db.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=? AND enabled=1", userID).Scan(&currentHash); err != nil || !verifyPassword(currentPassword, currentHash) {
		return ErrInvalidCredentials
	}
	newHash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=?", newHash, userID); err != nil {
		return err
	}
	if keepSessionID == "" {
		_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", userID)
	} else {
		_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=? AND id<>?", userID, keepSessionID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func validateCredentials(username, password string) (string, error) {
	username = strings.TrimSpace(username)
	if len(username) < 2 {
		return "", errors.New("username must be at least 2 characters")
	}
	if err := validatePassword(password); err != nil {
		return "", err
	}
	return username, nil
}

type scanner func(dest ...any) error

func scanUser(scan scanner) (User, error) {
	var user User
	var enabled int
	var lastLogin sql.NullInt64
	if err := scan(&user.ID, &user.Username, &enabled, &user.CreatedAt, &lastLogin); err != nil {
		return User{}, err
	}
	user.Enabled = enabled != 0
	if lastLogin.Valid {
		value := lastLogin.Int64
		user.LastLoginAt = &value
	}
	return user, nil
}
