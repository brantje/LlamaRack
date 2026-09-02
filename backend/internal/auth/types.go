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

const (
	APIKeyTypeInference  = "inference"
	APIKeyTypeManagement = "management"
	APIKeyTypeFull       = "full"

	OwnerKindUser           = "user"
	OwnerKindServiceAccount = "service_account"

	APIKeyStatusEnabled       = "enabled"
	APIKeyStatusDisabled      = "disabled"
	APIKeyStatusOwnerDisabled = "owner_disabled"
	APIKeyStatusExpired       = "expired"

	apiKeySecretPrefix = "sk-"
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

	ErrAPIKeyMissing              = errors.New("missing api key")
	ErrAPIKeyInvalid              = errors.New("invalid api key")
	ErrAPIKeyNameRequired         = errors.New("name is required")
	ErrAPIKeyTypeInvalid          = errors.New("key_type must be inference, management, or full")
	ErrAPIKeyOwnerRequired        = errors.New("exactly one of owner_user_id or owner_service_account_id is required")
	ErrAPIKeyOwnerDisabled        = errors.New("owner is disabled")
	ErrAPIKeyOwnerNotFound        = errors.New("owner not found")
	ErrAPIKeyInstancesNotAllowed  = errors.New("instance_ids are only valid for inference keys")
	ErrUnknownInstanceID          = errors.New("unknown instance id")
	ErrAPIKeyExpiresOnInvalid     = errors.New("expires_on must be a YYYY-MM-DD date")
	ErrAPIKeyExpiresOnPast        = errors.New("expires_on must not be in the past")
	ErrServiceAccountNameRequired = errors.New("name is required")
	ErrHiddenPrincipal            = errors.New("hidden principal")
	ErrManagedAPIKeyImmutable     = errors.New("this key's name and owner cannot be changed")
)

const ManagedPrincipalName = "LiteLLM"

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
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	Prefix             string   `json:"prefix"`
	KeyType            string   `json:"key_type"`
	OwnerKind          string   `json:"owner_kind"`
	OwnerID            string   `json:"owner_id"`
	OwnerName          string   `json:"owner_name"`
	OwnerEnabled       bool     `json:"owner_enabled"`
	Enabled            bool     `json:"enabled"`
	Status             string   `json:"status"`
	InstanceIDs        []string `json:"instance_ids"`
	MissingInstanceIDs []string `json:"missing_instance_ids,omitempty"`
	ExpiresOn          *string  `json:"expires_on,omitempty"`
	CreatedByUserID    *int64   `json:"created_by_user_id,omitempty"`
	CreatedAt          int64    `json:"created_at"`
	LastUsedAt         *int64   `json:"last_used_at,omitempty"`
	HiddenOwner        bool     `json:"-"`
	Managed            bool     `json:"managed,omitempty"`
}

type CreateAPIKeyInput struct {
	Name                  string
	KeyType               string
	OwnerUserID           *int64
	OwnerServiceAccountID string
	InstanceIDs           []string
	ExpiresOn             string
	ClearExpiresOn        bool
	CreatedByUserID       *int64
}

type UpdateAPIKeyInput struct {
	Name                  *string
	OwnerUserID           *int64
	OwnerServiceAccountID *string
	InstanceIDs           *[]string
	ExpiresOn             *string
	ClearExpiresOn        bool
	Enabled               *bool
}

type ServiceAccount struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	Hidden          bool     `json:"-"`
	CreatedAt       int64    `json:"created_at"`
	CreatedByUserID *int64   `json:"created_by_user_id,omitempty"`
	Keys            []APIKey `json:"-"`
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
