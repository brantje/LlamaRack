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
	"time"

	"golang.org/x/crypto/argon2"
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Enabled  bool   `json:"enabled"`
}

type APIKey struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at,omitempty"`
}

type Service struct {
	db              *sql.DB
	sessionLifetime time.Duration
}

func New(db *sql.DB, sessionLifetime time.Duration) *Service {
	return &Service{db: db, sessionLifetime: sessionLifetime}
}

func (s *Service) BootstrapRequired(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

func (s *Service) Bootstrap(ctx context.Context, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if len(username) < 2 {
		return User{}, errors.New("username must be at least 2 characters")
	}
	if len(password) < 10 {
		return User{}, errors.New("password must be at least 10 characters")
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
	res, err := tx.ExecContext(ctx, "INSERT INTO users(username,password_hash,role) VALUES(?,?,'admin')", username, hash)
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, Role: "admin", Enabled: true}, nil
}

func (s *Service) Login(ctx context.Context, username, password string) (string, User, error) {
	var user User
	var hash string
	var enabled int
	err := s.db.QueryRowContext(ctx, "SELECT id,username,password_hash,role,enabled FROM users WHERE username=?", strings.TrimSpace(username)).Scan(&user.ID, &user.Username, &hash, &user.Role, &enabled)
	if err != nil || !verifyPassword(password, hash) || enabled == 0 {
		return "", User{}, errors.New("invalid credentials")
	}
	user.Enabled = true
	token, err := randomToken(32)
	if err != nil {
		return "", User{}, err
	}
	id, err := randomToken(16)
	if err != nil {
		return "", User{}, err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)", id, user.ID, tokenHash(token), time.Now().Add(s.sessionLifetime).Unix())
	return token, user, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash(token))
	return err
}

func (s *Service) SessionUser(ctx context.Context, token string) (User, error) {
	var user User
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.role,u.enabled FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>?`, tokenHash(token), time.Now().Unix()).Scan(&user.ID, &user.Username, &user.Role, &enabled)
	if err != nil || enabled == 0 {
		return User{}, errors.New("session invalid")
	}
	user.Enabled = true
	return user, nil
}

func CanOperate(role string) bool { return role == "admin" || role == "operator" }
func IsAdmin(role string) bool     { return role == "admin" }

func (s *Service) CreateAPIKey(ctx context.Context, name string) (APIKey, string, error) {
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
	_, err = s.db.ExecContext(ctx, "INSERT INTO api_keys(id,name,prefix,token_hash) VALUES(?,?,?,?)", id, name, prefix, tokenHash(secret))
	if err != nil {
		return APIKey{}, "", err
	}
	return APIKey{ID: id, Name: name, Prefix: prefix, Enabled: true, CreatedAt: time.Now().Unix()}, secret, nil
}

func (s *Service) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,prefix,enabled,created_at,last_used_at FROM api_keys ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var k APIKey
		var enabled int
		var last sql.NullInt64
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &enabled, &k.CreatedAt, &last); err != nil {
			return nil, err
		}
		k.Enabled = enabled != 0
		if last.Valid {
			v := last.Int64
			k.LastUsedAt = &v
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Service) RevokeAPIKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE api_keys SET enabled=0 WHERE id=?", id)
	return err
}
func (s *Service) AuthenticateAPIKey(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("missing api key")
	}
	res, err := s.db.ExecContext(ctx, "UPDATE api_keys SET last_used_at=? WHERE token_hash=? AND enabled=1", time.Now().Unix(), tokenHash(token))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("invalid api key")
	}
	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return fmt.Sprintf("argon2id$v=19$m=65536,t=3,p=2$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 {
		return false
	}
	params := strings.Split(parts[2], ",")
	if len(params) != 3 {
		return false
	}
	m64, _ := strconv.ParseUint(strings.TrimPrefix(params[0], "m="), 10, 32)
	t64, _ := strconv.ParseUint(strings.TrimPrefix(params[1], "t="), 10, 32)
	p64, _ := strconv.ParseUint(strings.TrimPrefix(params[2], "p="), 10, 8)
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[3])
	expected, err2 := base64.RawStdEncoding.DecodeString(parts[4])
	if err1 != nil || err2 != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, uint32(t64), uint32(m64), uint8(p64), uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
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
