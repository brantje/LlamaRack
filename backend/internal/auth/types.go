package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"sync"
	"time"
)

const (
	passwordMinLength = 10
	argonMemory       = 64 * 1024
	argonTime         = 3
	argonThreads      = 2
	argonKeyLength    = 32
	apiUseWriteEvery  = 30 * time.Second
	managerIssuer     = "llamarack"
	wsTicketLifetime  = 30 * time.Second
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrLastEnabledUser    = errors.New("cannot disable or delete the last enabled management user")
	ErrSelfDelete         = errors.New("cannot delete the current management user")
	ErrSessionInvalid     = errors.New("session invalid")
	ErrCSRFInvalid        = errors.New("csrf token invalid")
	ErrOIDCLinkRequired   = errors.New("external identity is not linked to a management user")
	ErrOIDCUsernameTaken  = errors.New("OIDC username matches an existing account; explicit linking is required")
	ErrAuthLockoutRisk    = errors.New("operation would leave no usable management login method")
)

type User struct {
	ID             int64  `json:"id"`
	Username       string `json:"username"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      int64  `json:"created_at"`
	LastLoginAt    *int64 `json:"last_login_at,omitempty"`
	ActiveSessions int    `json:"active_sessions,omitempty"`
}

type Session struct {
	ID            string `json:"id"`
	UserID        int64  `json:"user_id"`
	CreatedAt     int64  `json:"created_at"`
	ExpiresAt     int64  `json:"expires_at"`
	RemoteAddress string `json:"remote_address"`
	UserAgent     string `json:"user_agent"`
	Current       bool   `json:"current,omitempty"`
	JTI           string `json:"-"`
}

type LoginResult struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresAt   int64  `json:"expires_at"`
	User        User   `json:"user"`
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

type wsTicket struct {
	SessionID string
	JTI       string
	ExpiresAt time.Time
}

type Service struct {
	db *sql.DB

	mu              sync.RWMutex
	sessionLifetime time.Duration
	lastAPIKeyWrite map[string]time.Time
	jwtPrivate      ed25519.PrivateKey
	jwtPublic       ed25519.PublicKey
	schemaErr       error

	ticketMu  sync.Mutex
	wsTickets map[string]wsTicket
}

func New(db *sql.DB, sessionLifetime time.Duration) *Service {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic("generate management signing key: " + err.Error())
	}
	return &Service{
		db: db, sessionLifetime: sessionLifetime, lastAPIKeyWrite: map[string]time.Time{},
		jwtPrivate: privateKey, jwtPublic: publicKey, wsTickets: map[string]wsTicket{},
		schemaErr: ensureAuthSchema(db),
	}
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
