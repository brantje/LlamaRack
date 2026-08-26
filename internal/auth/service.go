package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleReadonly Role = "readonly"
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     Role   `json:"role"`
	Enabled  bool   `json:"enabled"`
}

type Service struct {
	db              *sql.DB
	sessionLifetime time.Duration
}

func New(db *sql.DB, sessionLifetime time.Duration) *Service {
	return &Service{db: db, sessionLifetime: sessionLifetime}
}

func (s *Service) BootstrapRequired(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func (s *Service) BootstrapAdmin(ctx context.Context, username, password string) (User, error) {
	required, err := s.BootstrapRequired(ctx)
	if err != nil {
		return User{}, err
	}
	if !required {
		return User{}, errors.New("bootstrap is already complete")
	}
	username = strings.TrimSpace(username)
	if len(username) < 2 || len(password) < 10 {
		return User{}, errors.New("username must be at least 2 characters and password at least 10")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	result, err := s.db.ExecContext(ctx, "INSERT INTO users(username,password_hash,role) VALUES(?,?,?)", username, hash, RoleAdmin)
	if err != nil {
		return User{}, err
	}
	id, _ := result.LastInsertId()
	return User{ID: id, Username: username, Role: RoleAdmin, Enabled: true}, nil
}

func (s *Service) Login(ctx context.Context, username, password string) (string, User, error) {
	var user User
	var passwordHash string
	var enabled int
	err := s.db.QueryRowContext(ctx, "SELECT id,username,password_hash,role,enabled FROM users WHERE username=?", strings.TrimSpace(username)).Scan(&user.ID, &user.Username, &passwordHash, &user.Role, &enabled)
	if err != nil || !verifyPassword(password, passwordHash) || enabled == 0 {
		return "", User{}, errors.New("invalid username or password")
	}
	user.Enabled = true
	token, err := randomToken(32)
	if err != nil {
		return "", User{}, err
	}
	sum := sha256.Sum256([]byte(token))
	id, err := randomToken(18)
	if err != nil {
		return "", User{}, err
	}
	expires := time.Now().UTC().Add(s.sessionLifetime)
	if _, err := s.db.ExecContext(ctx, "INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)", id, user.ID, hex.EncodeToString(sum[:]), expires.Format(time.RFC3339Nano)); err != nil {
		return "", User{}, err
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE users SET last_login_at=CURRENT_TIMESTAMP WHERE id=?", user.ID)
	return token, user, nil
}

func (s *Service) SessionUser(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, errors.New("missing session")
	}
	sum := sha256.Sum256([]byte(token))
	var user User
	var enabled int
	var expiresRaw string
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.role,u.enabled,s.expires_at
FROM sessions s JOIN users u ON u.id=s.user_id
WHERE s.token_hash=?`, hex.EncodeToString(sum[:])).Scan(&user.ID, &user.Username, &user.Role, &enabled, &expiresRaw)
	if err != nil || enabled == 0 {
		return User{}, errors.New("invalid session")
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresRaw)
	if err != nil || time.Now().After(expires) {
		return User{}, errors.New("session expired")
	}
	user.Enabled = true
	return user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	sum := sha256.Sum256([]byte(token))
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", hex.EncodeToString(sum[:]))
	return err
}

type APIKey struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	CreatedAt string `json:"created_at,omitempty"`
	Enabled   bool   `json:"enabled"`
}

func (s *Service) CreateAPIKey(ctx context.Context, name string, createdBy int64) (APIKey, string, error) {
	secret, err := randomToken(32)
	if err != nil {
		return APIKey{}, "", err
	}
	plaintext := "sk-lcm-" + secret
	sum := sha256.Sum256([]byte(plaintext))
	id, err := randomToken(12)
	if err != nil {
		return APIKey{}, "", err
	}
	prefix := plaintext
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO api_keys(id,name,key_prefix,key_hash,created_by_user_id) VALUES(?,?,?,?,?)", id, strings.TrimSpace(name), prefix, hex.EncodeToString(sum[:]), createdBy); err != nil {
		return APIKey{}, "", err
	}
	return APIKey{ID: id, Name: name, Prefix: prefix, Enabled: true}, plaintext, nil
}

func (s *Service) AuthenticateAPIKey(ctx context.Context, plaintext string) error {
	if !strings.HasPrefix(plaintext, "sk-lcm-") {
		return errors.New("invalid api key")
	}
	sum := sha256.Sum256([]byte(plaintext))
	var id string
	var enabled int
	err := s.db.QueryRowContext(ctx, "SELECT id,enabled FROM api_keys WHERE key_hash=?", hex.EncodeToString(sum[:])).Scan(&id, &enabled)
	if err != nil || enabled == 0 {
		return errors.New("invalid api key")
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE api_keys SET last_used_at=CURRENT_TIMESTAMP WHERE id=?", id)
	return nil
}

func (s *Service) ListAPIKeys(ctx context.Context) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,key_prefix,created_at,enabled FROM api_keys ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []APIKey
	for rows.Next() {
		var item APIKey
		var enabled int
		if err := rows.Scan(&item.ID, &item.Name, &item.Prefix, &item.CreatedAt, &enabled); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) RevokeAPIKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE api_keys SET enabled=0, revoked_at=CURRENT_TIMESTAMP WHERE id=?", id)
	return err
}

func CanOperate(role Role) bool { return role == RoleAdmin || role == RoleOperator }
func IsAdmin(role Role) bool    { return role == RoleAdmin }

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const memory = 64 * 1024
	const iterations = 3
	const parallelism = 2
	const keyLen = 32
	hash := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", memory, iterations, parallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false
	}
	memory64, err1 := strconv.ParseUint(strings.TrimPrefix(params[0], "m="), 10, 32)
	iterations64, err2 := strconv.ParseUint(strings.TrimPrefix(params[1], "t="), 10, 32)
	parallel64, err3 := strconv.ParseUint(strings.TrimPrefix(params[2], "p="), 10, 8)
	salt, err4 := base64.RawStdEncoding.DecodeString(parts[4])
	expected, err5 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, uint32(iterations64), uint32(memory64), uint8(parallel64), uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
