package auth

import (
	"context"
	"database/sql"

	"github.com/brantje/llamarack/backend/internal/database"
)

type sessionCreate struct {
	ID            string
	UserID        int64
	TokenHash     string
	CSRFTokenHash string
	CreatedAt     int64
	ExpiresAt     int64
	RemoteAddress string
	UserAgent     string
}

// SessionStore owns credential/session persistence and login transactions.
type SessionStore interface {
	Credentials(context.Context, string) (User, string, bool, error)
	CommitLogin(context.Context, int64, string, string, int64, *sessionCreate) error
	Insert(context.Context, sessionCreate) error
	DeleteByTokenHash(context.Context, string) error
	ResolveByTokenHash(context.Context, string, int64) (User, Session, error)
	ResolveByIdentity(context.Context, int64, string, string, int64) (User, Session, error)
	ValidateCSRF(context.Context, string, string, int64) (bool, error)
	List(context.Context, int64, int64) ([]Session, error)
	Revoke(context.Context, string) error
	RevokeOwn(context.Context, int64, string) error
	RevokeOther(context.Context, int64, string) (int64, error)
	RevokeAll(context.Context, int64) (int64, error)
	UserIDForIdentity(context.Context, string, string) (int64, error)
}

type sqlSessionStore struct{ db database.Store }

func NewSessionStore(db database.Store) SessionStore { return &sqlSessionStore{db: db} }

func (s *sqlSessionStore) Credentials(ctx context.Context, username string) (User, string, bool, error) {
	var user User
	var hash string
	var enabled int
	var lastLogin sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id,username,password_hash,enabled,created_at,last_login_at FROM users WHERE username=?`, username).
		Scan(&user.ID, &user.Username, &hash, &enabled, &user.CreatedAt, &lastLogin)
	if err != nil {
		return User{}, "", false, database.ClassifyError(err)
	}
	user.Enabled = enabled != 0
	if lastLogin.Valid {
		value := lastLogin.Int64
		user.LastLoginAt = &value
	}
	return user, hash, user.Enabled, nil
}

func (s *sqlSessionStore) CommitLogin(ctx context.Context, userID int64, originalHash, rehashed string, now int64, session *sessionCreate) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if rehashed != "" {
		result, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=? AND password_hash=?`, rehashed, userID, originalHash)
		if err != nil {
			return database.ClassifyError(err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return database.ClassifyError(err)
		}
		if n != 1 {
			return ErrInvalidCredentials
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at=? WHERE id=?`, now, userID); err != nil {
		return database.ClassifyError(err)
	}
	if session != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions(id,user_id,token_hash,csrf_token_hash,created_at,expires_at,remote_address,user_agent) VALUES(?,?,?,?,?,?,?,?)`,
			session.ID, session.UserID, session.TokenHash, session.CSRFTokenHash, session.CreatedAt, session.ExpiresAt, session.RemoteAddress, session.UserAgent); err != nil {
			return database.ClassifyError(err)
		}
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlSessionStore) Insert(ctx context.Context, session sessionCreate) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id,user_id,token_hash,csrf_token_hash,created_at,expires_at,remote_address,user_agent) VALUES(?,?,?,?,?,?,?,?)`,
		session.ID, session.UserID, session.TokenHash, session.CSRFTokenHash, session.CreatedAt, session.ExpiresAt, session.RemoteAddress, session.UserAgent)
	return database.ClassifyError(err)
}

func (s *sqlSessionStore) DeleteByTokenHash(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hash)
	return database.ClassifyError(err)
}

func (s *sqlSessionStore) ResolveByTokenHash(ctx context.Context, hash string, now int64) (User, Session, error) {
	return scanStoredSession(s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.enabled,u.created_at,u.last_login_at,s.id,s.user_id,s.created_at,s.expires_at,s.remote_address,s.user_agent
		FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?`, hash, now))
}

func (s *sqlSessionStore) ResolveByIdentity(ctx context.Context, userID int64, sessionID, hash string, now int64) (User, Session, error) {
	return scanStoredSession(s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.enabled,u.created_at,u.last_login_at,s.id,s.user_id,s.created_at,s.expires_at,s.remote_address,s.user_agent
		FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.id=? AND s.user_id=? AND s.token_hash=? AND s.expires_at>?`,
		sessionID, userID, hash, now))
}

func scanStoredSession(row interface{ Scan(...any) error }) (User, Session, error) {
	var user User
	var session Session
	var enabled int
	var lastLogin sql.NullInt64
	if err := row.Scan(&user.ID, &user.Username, &enabled, &user.CreatedAt, &lastLogin,
		&session.ID, &session.UserID, &session.CreatedAt, &session.ExpiresAt, &session.RemoteAddress, &session.UserAgent); err != nil {
		return User{}, Session{}, database.ClassifyError(err)
	}
	if enabled == 0 {
		return User{}, Session{}, ErrSessionInvalid
	}
	user.Enabled = true
	if lastLogin.Valid {
		value := lastLogin.Int64
		user.LastLoginAt = &value
	}
	return user, session, nil
}

func (s *sqlSessionStore) ValidateCSRF(ctx context.Context, tokenHashValue, csrfHash string, now int64) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.csrf_token_hash=? AND s.expires_at>? AND u.enabled=1`, tokenHashValue, csrfHash, now).Scan(&count); err != nil {
		return false, database.ClassifyError(err)
	}
	return count == 1, nil
}

func (s *sqlSessionStore) List(ctx context.Context, userID, now int64) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,created_at,expires_at,remote_address,user_agent FROM sessions WHERE user_id=? AND expires_at>? ORDER BY created_at DESC`, userID, now)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	var items []Session
	for rows.Next() {
		var item Session
		if err := rows.Scan(&item.ID, &item.UserID, &item.CreatedAt, &item.ExpiresAt, &item.RemoteAddress, &item.UserAgent); err != nil {
			return nil, database.ClassifyError(err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return items, nil
}

func (s *sqlSessionStore) Revoke(ctx context.Context, id string) error {
	return deleteSessionOne(ctx, s.db, `DELETE FROM sessions WHERE id=?`, id)
}

func (s *sqlSessionStore) RevokeOwn(ctx context.Context, userID int64, id string) error {
	return deleteSessionOne(ctx, s.db, `DELETE FROM sessions WHERE id=? AND user_id=?`, id, userID)
}

func deleteSessionOne(ctx context.Context, db database.Store, query string, args ...any) error {
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if n != 1 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	return nil
}

func (s *sqlSessionStore) RevokeOther(ctx context.Context, userID int64, keep string) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND id<>?`, userID, keep)
	if err != nil {
		return 0, database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	return n, database.ClassifyError(err)
}

func (s *sqlSessionStore) RevokeAll(ctx context.Context, userID int64) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
	if err != nil {
		return 0, database.ClassifyError(err)
	}
	n, err := result.RowsAffected()
	return n, database.ClassifyError(err)
}

func (s *sqlSessionStore) UserIDForIdentity(ctx context.Context, sessionID, hash string) (int64, error) {
	var userID int64
	if err := s.db.QueryRowContext(ctx, `SELECT user_id FROM sessions WHERE id=? AND token_hash=?`, sessionID, hash).Scan(&userID); err != nil {
		return 0, database.ClassifyError(err)
	}
	return userID, nil
}

var _ SessionStore = (*sqlSessionStore)(nil)
