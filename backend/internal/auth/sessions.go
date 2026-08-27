package auth

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s *Service) LoginWithMetadata(ctx context.Context, username, password, remoteAddress, userAgent string) (string, string, User, error) {
	var user User
	var hash string
	var enabled int
	var lastLogin sql.NullInt64
	if err := s.db.QueryRowContext(ctx, "SELECT id,username,password_hash,enabled,created_at,last_login_at FROM users WHERE username=?", strings.TrimSpace(username)).Scan(&user.ID, &user.Username, &hash, &enabled, &user.CreatedAt, &lastLogin); err != nil || enabled == 0 || !verifyPassword(password, hash) {
		return "", "", User{}, ErrInvalidCredentials
	}
	user.Enabled = true
	if lastLogin.Valid {
		value := lastLogin.Int64
		user.LastLoginAt = &value
	}
	token, err := randomToken(32)
	if err != nil {
		return "", "", User{}, err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return "", "", User{}, err
	}
	id, err := randomToken(16)
	if err != nil {
		return "", "", User{}, err
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", User{}, err
	}
	defer tx.Rollback()
	if passwordNeedsRehash(hash) {
		rehashed, err := hashPassword(password)
		if err != nil {
			return "", "", User{}, err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=?", rehashed, user.ID); err != nil {
			return "", "", User{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET last_login_at=? WHERE id=?", now.Unix(), user.ID); err != nil {
		return "", "", User{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,user_id,token_hash,csrf_token_hash,created_at,expires_at,remote_address,user_agent) VALUES(?,?,?,?,?,?,?,?)`, id, user.ID, tokenHash(token), tokenHash(csrf), now.Unix(), now.Add(s.SessionLifetime()).Unix(), strings.TrimSpace(remoteAddress), truncate(userAgent, 512)); err != nil {
		return "", "", User{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", "", User{}, err
	}
	last := now.Unix()
	user.LastLoginAt = &last
	return token, csrf, user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash(token))
	return err
}

func (s *Service) SessionUser(ctx context.Context, token string) (User, error) {
	user, _, err := s.SessionUserWithSession(ctx, token)
	return user, err
}

func (s *Service) SessionUserWithSession(ctx context.Context, token string) (User, Session, error) {
	var user User
	var session Session
	var enabled int
	var lastLogin sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.enabled,u.created_at,u.last_login_at,s.id,s.user_id,s.created_at,s.expires_at,s.remote_address,s.user_agent
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.expires_at>?`, tokenHash(token), time.Now().Unix()).Scan(
		&user.ID, &user.Username, &enabled, &user.CreatedAt, &lastLogin,
		&session.ID, &session.UserID, &session.CreatedAt, &session.ExpiresAt, &session.RemoteAddress, &session.UserAgent,
	)
	if err != nil || enabled == 0 {
		return User{}, Session{}, ErrSessionInvalid
	}
	user.Enabled = true
	if lastLogin.Valid {
		value := lastLogin.Int64
		user.LastLoginAt = &value
	}
	session.Current = true
	return user, session, nil
}

func (s *Service) ValidateCSRF(ctx context.Context, sessionToken, csrfToken string) error {
	if sessionToken == "" || csrfToken == "" {
		return ErrCSRFInvalid
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.csrf_token_hash=? AND s.expires_at>? AND u.enabled=1`, tokenHash(sessionToken), tokenHash(csrfToken), time.Now().Unix()).Scan(&count); err != nil || count != 1 {
		return ErrCSRFInvalid
	}
	return nil
}

func (s *Service) ListSessions(ctx context.Context, userID int64, currentSessionID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,created_at,expires_at,remote_address,user_agent FROM sessions WHERE user_id=? AND expires_at>? ORDER BY created_at DESC`, userID, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Session, 0)
	for rows.Next() {
		var item Session
		if err := rows.Scan(&item.ID, &item.UserID, &item.CreatedAt, &item.ExpiresAt, &item.RemoteAddress, &item.UserAgent); err != nil {
			return nil, err
		}
		item.Current = currentSessionID != "" && item.ID == currentSessionID
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) RevokeSession(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id=?", id)
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
	return nil
}

func (s *Service) RevokeOtherSessions(ctx context.Context, userID int64, keepSessionID string) (int64, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=? AND id<>?", userID, keepSessionID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Service) RevokeAllSessions(ctx context.Context, userID int64) (int64, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", userID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
