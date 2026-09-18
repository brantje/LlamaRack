package auth

import (
	"context"
	"database/sql"

	"github.com/brantje/llamarack/backend/internal/database"
)

const authUserInvariantLockID int64 = 0x4c4c41555448

// UserStore is the management-user persistence boundary. It owns all SQL,
// transaction, and concurrency semantics needed to preserve user invariants.
type UserStore interface {
	BootstrapRequired(context.Context) (bool, error)
	Bootstrap(context.Context, string, string, int64) (User, error)
	Create(context.Context, string, string, int64) (User, error)
	List(context.Context, int64) ([]User, error)
	ByID(context.Context, int64) (User, error)
	SetEnabled(context.Context, int64, bool) error
	Delete(context.Context, int64) error
	EnabledPasswordHash(context.Context, int64) (string, error)
	ResetPassword(context.Context, int64, string) error
	ChangePassword(context.Context, int64, string, string) error
}

type sqlUserStore struct {
	db database.Store
}

// NewUserStore constructs the SQL/GORM-backed management-user adapter.
func NewUserStore(db database.Store) UserStore {
	return &sqlUserStore{db: db}
}

func (s *sqlUserStore) BootstrapRequired(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return false, database.ClassifyError(err)
	}
	return count == 0, nil
}

func (s *sqlUserStore) Bootstrap(ctx context.Context, username, passwordHash string, createdAt int64) (User, error) {
	tx, err := s.beginInvariantTx(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return User{}, database.ClassifyError(err)
	}
	if count != 0 {
		return User{}, ErrBootstrapCompleted
	}
	var id int64
	if err := tx.QueryRowContext(ctx, "INSERT INTO users(username,password_hash,created_at) VALUES(?,?,?) RETURNING id", username, passwordHash, createdAt).Scan(&id); err != nil {
		return User{}, database.ClassifyError(err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, database.ClassifyError(err)
	}
	return User{ID: id, Username: username, Enabled: true, CreatedAt: createdAt}, nil
}

func (s *sqlUserStore) Create(ctx context.Context, username, passwordHash string, createdAt int64) (User, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx, "INSERT INTO users(username,password_hash,created_at) VALUES(?,?,?) RETURNING id", username, passwordHash, createdAt).Scan(&id); err != nil {
		return User{}, database.ClassifyError(err)
	}
	return User{ID: id, Username: username, Enabled: true, CreatedAt: createdAt}, nil
}

func (s *sqlUserStore) List(ctx context.Context, activeAfter int64) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.id,u.username,u.enabled,u.created_at,u.last_login_at,
		(SELECT COUNT(*) FROM sessions ss WHERE ss.user_id=u.id AND ss.expires_at>?)
		FROM users u ORDER BY LOWER(u.username),u.username`, activeAfter)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		user, err := scanStoredUser(rows, true)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return users, nil
}

func (s *sqlUserStore) ByID(ctx context.Context, id int64) (User, error) {
	return scanStoredUser(s.db.QueryRowContext(ctx, "SELECT id,username,enabled,created_at,last_login_at FROM users WHERE id=?", id), false)
}

func (s *sqlUserStore) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	tx, err := s.beginInvariantTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM users WHERE id=?", id).Scan(&current); err != nil {
		return database.ClassifyError(err)
	}
	if !enabled && current != 0 {
		var enabledCount int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE enabled=1").Scan(&enabledCount); err != nil {
			return database.ClassifyError(err)
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
		return database.ClassifyError(err)
	}
	if !enabled {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id); err != nil {
			return database.ClassifyError(err)
		}
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlUserStore) Delete(ctx context.Context, id int64) error {
	tx, err := s.beginInvariantTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var enabled int
	if err := tx.QueryRowContext(ctx, "SELECT enabled FROM users WHERE id=?", id).Scan(&enabled); err != nil {
		return database.ClassifyError(err)
	}
	if enabled != 0 {
		var enabledCount int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE enabled=1").Scan(&enabledCount); err != nil {
			return database.ClassifyError(err)
		}
		if enabledCount <= 1 {
			return ErrLastEnabledUser
		}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id=?", id)
	if err != nil {
		return database.ClassifyError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if rows != 1 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlUserStore) EnabledPasswordHash(ctx context.Context, id int64) (string, error) {
	var hash string
	if err := s.db.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=? AND enabled=1", id).Scan(&hash); err != nil {
		return "", database.ClassifyError(err)
	}
	return hash, nil
}

func (s *sqlUserStore) ResetPassword(ctx context.Context, id int64, passwordHash string) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=?", passwordHash, id)
	if err != nil {
		return database.ClassifyError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if rows != 1 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id); err != nil {
		return database.ClassifyError(err)
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlUserStore) ChangePassword(ctx context.Context, id int64, passwordHash, keepSessionID string) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=?", passwordHash, id)
	if err != nil {
		return database.ClassifyError(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if rows != 1 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	if keepSessionID == "" {
		_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", id)
	} else {
		_, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=? AND id<>?", id, keepSessionID)
	}
	if err != nil {
		return database.ClassifyError(err)
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlUserStore) beginInvariantTx(ctx context.Context) (database.Transaction, error) {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if dialect, ok := s.db.(interface{ Dialect() database.Dialect }); ok && dialect.Dialect() == database.DialectPostgres {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(?)", authUserInvariantLockID); err != nil {
			_ = tx.Rollback()
			return nil, database.ClassifyError(err)
		}
	}
	return tx, nil
}

type storedUserRow interface {
	Scan(...any) error
}

func scanStoredUser(row storedUserRow, withActiveSessions bool) (User, error) {
	var user User
	var enabled int
	var lastLogin sql.NullInt64
	var err error
	if withActiveSessions {
		err = row.Scan(&user.ID, &user.Username, &enabled, &user.CreatedAt, &lastLogin, &user.ActiveSessions)
	} else {
		err = row.Scan(&user.ID, &user.Username, &enabled, &user.CreatedAt, &lastLogin)
	}
	if err != nil {
		return User{}, database.ClassifyError(err)
	}
	user.Enabled = enabled != 0
	if lastLogin.Valid {
		value := lastLogin.Int64
		user.LastLoginAt = &value
	}
	return user, nil
}

var _ UserStore = (*sqlUserStore)(nil)
