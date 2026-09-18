package auth

import (
		"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	signingKeyFilename            = "management-jwt-ed25519.key"
	maxWebSocketTicketsPerSession = 8
	maxWebSocketTicketsGlobal     = 1024
)

type managementClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
}

func (s *Service) UsePersistentSigningKey(dataDir string) error {
	if s.schemaErr != nil {
		return s.schemaErr
	}
	path := filepath.Join(dataDir, signingKeyFilename)
	privateKey, err := loadOrCreateEd25519Key(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.jwtPrivate = privateKey
	s.jwtPublic = privateKey.Public().(ed25519.PublicKey)
	s.mu.Unlock()
	return nil
}

func loadOrCreateEd25519Key(path string) (ed25519.PrivateKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		switch len(data) {
		case ed25519.SeedSize:
			return ed25519.NewKeyFromSeed(data), nil
		case ed25519.PrivateKeySize:
			return ed25519.PrivateKey(append([]byte(nil), data...)), nil
		default:
			return nil, fmt.Errorf("management signing key has invalid length %d", len(data))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := io.ReadFull(rand.Reader, seed); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadOrCreateEd25519Key(path)
		}
		return nil, err
	}
	if _, err := file.Write(seed); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func (s *Service) LoginBearerWithMetadata(ctx context.Context, username, password, remoteAddress, userAgent string) (LoginResult, error) {
	work, err := reservePasswordWork()
	if err != nil {
		return LoginResult{}, err
	}
	defer work.Release()

	user, hash, err := s.verifyLoginCredentials(ctx, work, username, password)
	if err != nil {
		return LoginResult{}, err
	}

	var rehashed string
	if passwordNeedsRehash(hash) {
		rehashed, err = hashPasswordWithReservation(ctx, work, password)
		if err != nil {
			return LoginResult{}, err
		}
	}
	work.Release()

	now := time.Now()
	if err := s.sessions.CommitLogin(ctx, user.ID, hash, rehashed, now.Unix(), nil); err != nil {
		return LoginResult{}, err
	}
	last := now.Unix()
	user.LastLoginAt = &last
	return s.CreateBearerSession(ctx, user, remoteAddress, userAgent)
}

func (s *Service) CreateBearerSession(ctx context.Context, user User, remoteAddress, userAgent string) (LoginResult, error) {
	if user.ID <= 0 || !user.Enabled {
		return LoginResult{}, ErrSessionInvalid
	}
	sessionID, err := randomToken(18)
	if err != nil {
		return LoginResult{}, err
	}
	jti, err := randomToken(24)
	if err != nil {
		return LoginResult{}, err
	}
	now := time.Now()
	expiresAt := now.Add(s.SessionLifetime())
	claims := managementClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    managerIssuer,
			Subject:   strconv.FormatInt(user.ID, 10),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        jti,
		},
		SessionID: sessionID,
	}
	s.mu.RLock()
	privateKey := append(ed25519.PrivateKey(nil), s.jwtPrivate...)
	s.mu.RUnlock()
	if len(privateKey) != ed25519.PrivateKeySize {
		return LoginResult{}, errors.New("management signing key is unavailable")
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(privateKey)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.sessions.Insert(ctx, sessionCreate{
		ID: sessionID, UserID: user.ID, TokenHash: tokenHash(jti), CreatedAt: now.Unix(), ExpiresAt: expiresAt.Unix(),
		RemoteAddress: strings.TrimSpace(remoteAddress), UserAgent: truncate(userAgent, 512),
	}); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{AccessToken: token, TokenType: "Bearer", ExpiresAt: expiresAt.Unix(), User: user}, nil
}

func (s *Service) AuthenticateBearer(ctx context.Context, encoded string) (User, Session, error) {
	claims, err := s.parseManagementToken(encoded)
	if err != nil {
		return User{}, Session{}, ErrSessionInvalid
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 || claims.SessionID == "" || claims.ID == "" {
		return User{}, Session{}, ErrSessionInvalid
	}
	return s.sessionByIdentity(ctx, userID, claims.SessionID, claims.ID)
}

func (s *Service) parseManagementToken(encoded string) (managementClaims, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return managementClaims{}, ErrSessionInvalid
	}
	s.mu.RLock()
	publicKey := append(ed25519.PublicKey(nil), s.jwtPublic...)
	s.mu.RUnlock()
	claims := managementClaims{}
	parsed, err := jwt.ParseWithClaims(encoded, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodEdDSA {
			return nil, errors.New("unexpected management token signing method")
		}
		return publicKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodEdDSA.Alg()}), jwt.WithIssuer(managerIssuer), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !parsed.Valid || claims.Issuer != managerIssuer || claims.ExpiresAt == nil || claims.IssuedAt == nil {
		return managementClaims{}, ErrSessionInvalid
	}
	return claims, nil
}

func (s *Service) sessionByIdentity(ctx context.Context, userID int64, sessionID, jti string) (User, Session, error) {
	user, session, err := s.sessions.ResolveByIdentity(ctx, userID, sessionID, tokenHash(jti), time.Now().Unix())
	if err != nil {
		return User{}, Session{}, ErrSessionInvalid
	}
	session.JTI = jti
	session.Current = true
	return user, session, nil
}

func (s *Service) IssueWebSocketTicket(ctx context.Context, session Session) (string, int64, error) {
	if session.ID == "" || session.JTI == "" {
		return "", 0, ErrSessionInvalid
	}
	if _, _, err := s.sessionByIdentity(ctx, session.UserID, session.ID, session.JTI); err != nil {
		return "", 0, err
	}
	ticket, err := randomToken(24)
	if err != nil {
		return "", 0, err
	}
	expires := time.Now().Add(wsTicketLifetime)
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	now := time.Now()
	for key, item := range s.wsTickets {
		if !item.ExpiresAt.After(now) {
			delete(s.wsTickets, key)
		}
	}

	for {
		count := 0
		oldestKey := ""
		var oldestExpiry time.Time
		for key, item := range s.wsTickets {
			if item.SessionID != session.ID {
				continue
			}
			count++
			if oldestKey == "" || item.ExpiresAt.Before(oldestExpiry) {
				oldestKey, oldestExpiry = key, item.ExpiresAt
			}
		}
		if count < maxWebSocketTicketsPerSession {
			break
		}
		delete(s.wsTickets, oldestKey)
	}

	for len(s.wsTickets) >= maxWebSocketTicketsGlobal {
		oldestKey := ""
		var oldestExpiry time.Time
		for key, item := range s.wsTickets {
			if oldestKey == "" || item.ExpiresAt.Before(oldestExpiry) {
				oldestKey, oldestExpiry = key, item.ExpiresAt
			}
		}
		if oldestKey == "" {
			break
		}
		delete(s.wsTickets, oldestKey)
	}

	s.wsTickets[ticket] = wsTicket{SessionID: session.ID, JTI: session.JTI, ExpiresAt: expires}
	return ticket, expires.Unix(), nil
}

func (s *Service) ConsumeWebSocketTicket(ctx context.Context, ticket string) (User, Session, error) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return User{}, Session{}, ErrSessionInvalid
	}
	s.ticketMu.Lock()
	item, ok := s.wsTickets[ticket]
	delete(s.wsTickets, ticket)
	s.ticketMu.Unlock()
	if !ok || !item.ExpiresAt.After(time.Now()) {
		return User{}, Session{}, ErrSessionInvalid
	}
	userID, err := s.sessions.UserIDForIdentity(ctx, item.SessionID, tokenHash(item.JTI))
	if err != nil {
		return User{}, Session{}, ErrSessionInvalid
	}
	return s.sessionByIdentity(ctx, userID, item.SessionID, item.JTI)
}
