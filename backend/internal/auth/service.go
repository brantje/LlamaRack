package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	passwordMinLength = 10
	argonMemory       = 64 * 1024
	argonTime         = 3
	argonThreads      = 2
	argonKeyLength    = 32
	apiUseWriteEvery  = 30 * time.Second
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrLastEnabledUser    = errors.New("cannot disable or delete the last enabled management user")
	ErrSelfDelete         = errors.New("cannot delete the current management user")
	ErrSessionInvalid     = errors.New("session invalid")
	ErrCSRFInvalid        = errors.New("csrf token invalid")
)

type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   int64  `json:"created_at"`
	LastLoginAt *int64 `json:"last_login_at,omitempty"`
}

type Session struct {
	ID            string `json:"id"`
	UserID        int64  `json:"user_id"`
	CreatedAt     int64  `json:"created_at"`
	ExpiresAt     int64  `json:"expires_at"`
	RemoteAddress string `json:"remote_address"`
	UserAgent     string `json:"user_agent"`
	Current       bool   `json:"current,omitempty"`
}

type APIKey struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Prefix          string `json:"prefix"`
	Enabled         bool   `json:"enabled"`
	CreatedByUserID *int64 `json:"created_by_user_id,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	LastUsedAt      *int64 `json:"last_used_at,omitempty"`
	RevokedAt       *int64 `json:"revoked_at,omitempty"`
}

type Service struct {
	db *sql.DB

	mu              sync.RWMutex
	sessionLifetime time.Duration
	lastAPIKeyWrite map[string]time.Time
}

func New(db *sql.DB, sessionLifetime time.Duration) *Service {
	return &Service{db: db, sessionLifetime: sessionLifetime, lastAPIKeyWrite: map[string]time.Time{}}
}

func (s *Service) SetSessionLifetime(lifetime time.Duration) {
	if lifetime <= 0 {
		return
	}
	s.mu.Lock()
	s.sessionLifetime = lifetime
	s.mu.Unlock()
}

func (s *Service) SessionLifetime() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionLifetime
}

func (s *Service) BootstrapRequired(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
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
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return User{}, err
	}
	if n != 0 {
		return User{}, errors.New("bootstrap already completed")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, "INSERT INTO users(username,password_hash,created_at) VALUES(?,?,?)", username, hash, now)
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
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
	res, err := s.db.ExecContext(ctx, "INSERT INTO users(username,password_hash,created_at) VALUES(?,?,?)", username, hash, now)
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
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
		u, err := scanUser(rows.Scan)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
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
	res, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=?", hash, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
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

// Login is retained for existing callers. HTTP login should use LoginWithMetadata so
// session diagnostics and CSRF state are recorded.
func (s *Service) Login(ctx context.Context, username, password string) (string, User, error) {
	token, _, user, err := s.LoginWithMetadata(ctx, username, password, "", "")
	return token, user, err
}

func (s *Service) LoginWithMetadata(ctx context.Context, username, password, remoteAddress, userAgent string) (string, string, User, error) {
	var user User
	var hash string
	var enabled int
	var lastLogin sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT id,username,password_hash,enabled,created_at,last_login_at FROM users WHERE username=?", strings.TrimSpace(username)).Scan(&user.ID, &user.Username, &hash, &enabled, &user.CreatedAt, &lastLogin)
	if err != nil || enabled == 0 || !verifyPassword(password, hash) {
		return "", "", User{}, ErrInvalidCredentials
	}
	user.Enabled = true
	if lastLogin.Valid {
		v := lastLogin.Int64
		user.LastLoginAt = &v
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
	expiresAt := now.Add(s.SessionLifetime()).Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", User{}, err
	}
	defer tx.Rollback()
	if passwordNeedsRehash(hash) {
		rehashed, hashErr := hashPassword(password)
		if hashErr != nil {
			return "", "", User{}, hashErr
		}
		if _, err := tx.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=?", rehashed, user.ID); err != nil {
			return "", "", User{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET last_login_at=? WHERE id=?", now.Unix(), user.ID); err != nil {
		return "", "", User{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,user_id,token_hash,csrf_token_hash,created_at,expires_at,remote_address,user_agent) VALUES(?,?,?,?,?,?,?,?)`, id, user.ID, tokenHash(token), tokenHash(csrf), now.Unix(), expiresAt, strings.TrimSpace(remoteAddress), truncate(userAgent, 512))
	if err != nil {
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
		v := lastLogin.Int64
		user.LastLoginAt = &v
	}
	session.Current = true
	return user, session, nil
}

func (s *Service) ValidateCSRF(ctx context.Context, sessionToken, csrfToken string) error {
	if sessionToken == "" || csrfToken == "" {
		return ErrCSRFInvalid
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.csrf_token_hash=? AND s.expires_at>? AND u.enabled=1`, tokenHash(sessionToken), tokenHash(csrfToken), time.Now().Unix()).Scan(&n)
	if err != nil || n != 1 {
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
	out := make([]Session, 0)
	for rows.Next() {
		var item Session
		if err := rows.Scan(&item.ID, &item.UserID, &item.CreatedAt, &item.ExpiresAt, &item.RemoteAddress, &item.UserAgent); err != nil {
			return nil, err
		}
		item.Current = currentSessionID != "" && item.ID == currentSessionID
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) RevokeSession(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) RevokeOtherSessions(ctx context.Context, userID int64, keepSessionID string) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=? AND id<>?", userID, keepSessionID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Service) RevokeAllSessions(ctx context.Context, userID int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Service) CreateAPIKey(ctx context.Context, name string) (APIKey, string, error) {
	return s.CreateAPIKeyForUser(ctx, name, 0)
}

func (s *Service) CreateAPIKeyForUser(ctx context.Context, name string, creatorUserID int64) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	secret, err := randomToken(32)
	if err != nil {
		return APIKey{}, "", err
	}
	id, err := randomToken(12)
	if err != nil {
		return APIKey{}, "", err
	}
	prefix := secret
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	now := time.Now().Unix()
	var creator any
	var creatorPtr *int64
	if creatorUserID > 0 {
		creator = creatorUserID
		v := creatorUserID
		creatorPtr = &v
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO api_keys(id,name,prefix,token_hash,created_by_user_id,created_at) VALUES(?,?,?,?,?,?)", id, name, prefix, tokenHash(secret), creator, now)
	if err != nil {
		return APIKey{}, "", err
	}
	return APIKey{ID: id, Name: name, Prefix: prefix, Enabled: true, CreatedByUserID: creatorPtr, CreatedAt: now}, secret, nil
}

func (s *Service) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,prefix,enabled,created_by_user_id,created_at,last_used_at,revoked_at FROM api_keys ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]APIKey, 0)
	for rows.Next() {
		var k APIKey
		var enabled int
		var creator, last, revoked sql.NullInt64
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &enabled, &creator, &k.CreatedAt, &last, &revoked); err != nil {
			return nil, err
		}
		k.Enabled = enabled != 0
		if creator.Valid {
			v := creator.Int64
			k.CreatedByUserID = &v
		}
		if last.Valid {
			v := last.Int64
			k.LastUsedAt = &v
		}
		if revoked.Valid {
			v := revoked.Int64
			k.RevokedAt = &v
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Service) SetAPIKeyEnabled(ctx context.Context, id string, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	query := "UPDATE api_keys SET enabled=? WHERE id=?"
	if enabled {
		query += " AND revoked_at IS NULL"
	}
	res, err := s.db.ExecContext(ctx, query, value, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteAPIKey is retained for backwards-compatible direct service callers. Management
// routes use RevokeAPIKey so revoked metadata remains visible.
func (s *Service) DeleteAPIKey(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM api_keys WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) RevokeAPIKey(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE api_keys SET enabled=0, revoked_at=COALESCE(revoked_at,?) WHERE id=?", time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) RotateAPIKey(ctx context.Context, id string, creatorUserID int64) (APIKey, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APIKey{}, "", err
	}
	defer tx.Rollback()
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT name FROM api_keys WHERE id=? AND revoked_at IS NULL", id).Scan(&name); err != nil {
		return APIKey{}, "", err
	}
	secret, err := randomToken(32)
	if err != nil {
		return APIKey{}, "", err
	}
	newID, err := randomToken(12)
	if err != nil {
		return APIKey{}, "", err
	}
	prefix := secret
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	now := time.Now().Unix()
	var creator any
	var creatorPtr *int64
	if creatorUserID > 0 {
		creator = creatorUserID
		v := creatorUserID
		creatorPtr = &v
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO api_keys(id,name,prefix,token_hash,created_by_user_id,created_at) VALUES(?,?,?,?,?,?)", newID, name, prefix, tokenHash(secret), creator, now); err != nil {
		return APIKey{}, "", err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE api_keys SET enabled=0,revoked_at=? WHERE id=?", now, id); err != nil {
		return APIKey{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return APIKey{}, "", err
	}
	return APIKey{ID: newID, Name: name, Prefix: prefix, Enabled: true, CreatedByUserID: creatorPtr, CreatedAt: now}, secret, nil
}

func (s *Service) AuthenticateAPIKey(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("missing api key")
	}
	var id string
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM api_keys WHERE token_hash=? AND enabled=1 AND revoked_at IS NULL", tokenHash(token)).Scan(&id); err != nil {
		return errors.New("invalid api key")
	}
	now := time.Now()
	if !s.shouldPersistAPIUse(id, now) {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE api_keys SET last_used_at=? WHERE id=? AND enabled=1 AND revoked_at IS NULL", now.Unix(), id); err != nil {
		return err
	}
	return nil
}

func (s *Service) shouldPersistAPIUse(id string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if last, ok := s.lastAPIKeyWrite[id]; ok && now.Sub(last) < apiUseWriteEvery {
		return false
	}
	s.lastAPIKeyWrite[id] = now
	return true
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

func validatePassword(password string) error {
	if len(password) < passwordMinLength {
		return fmt.Errorf("password must be at least %d characters", passwordMinLength)
	}
	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLength)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func verifyPassword(password, encoded string) bool {
	memory, iterations, threads, salt, expected, ok := parsePasswordHash(encoded)
	if !ok {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func passwordNeedsRehash(encoded string) bool {
	memory, iterations, threads, _, expected, ok := parsePasswordHash(encoded)
	return !ok || memory != argonMemory || iterations != argonTime || threads != argonThreads || len(expected) != argonKeyLength
}

func parsePasswordHash(encoded string) (uint32, uint32, uint8, []byte, []byte, bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return 0, 0, 0, nil, nil, false
	}
	params := strings.Split(parts[2], ",")
	if len(params) != 3 {
		return 0, 0, 0, nil, nil, false
	}
	m64, errM := strconv.ParseUint(strings.TrimPrefix(params[0], "m="), 10, 32)
	t64, errT := strconv.ParseUint(strings.TrimPrefix(params[1], "t="), 10, 32)
	p64, errP := strconv.ParseUint(strings.TrimPrefix(params[2], "p="), 10, 8)
	if errM != nil || errT != nil || errP != nil || t64 < 1 || p64 < 1 || m64 < 8*p64 {
		return 0, 0, 0, nil, nil, false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[3])
	expected, err2 := base64.RawStdEncoding.DecodeString(parts[4])
	if err1 != nil || err2 != nil || len(salt) == 0 || len(expected) == 0 {
		return 0, 0, 0, nil, nil, false
	}
	return uint32(m64), uint32(t64), uint8(p64), salt, expected, true
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

type scanner func(dest ...any) error

func scanUser(scan scanner) (User, error) {
	var user User
	var enabled int
	var last sql.NullInt64
	if err := scan(&user.ID, &user.Username, &enabled, &user.CreatedAt, &last); err != nil {
		return User{}, err
	}
	user.Enabled = enabled != 0
	if last.Valid {
		v := last.Int64
		user.LastLoginAt = &v
	}
	return user, nil
}
