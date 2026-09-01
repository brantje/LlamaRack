package auth

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestBearerValidationAndTicketErrorBranches(t *testing.T) {
	ctx := t.Context()
	s := testService(t)
	if err := s.UsePersistentSigningKey(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateBearerSession(ctx, User{}, "", ""); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("invalid user bearer session err=%v", err)
	}
	if _, _, err := s.ConsumeWebSocketTicket(ctx, ""); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("empty websocket ticket err=%v", err)
	}
	if _, _, err := s.ConsumeWebSocketTicket(ctx, "missing"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("missing websocket ticket err=%v", err)
	}
	if _, _, err := s.IssueWebSocketTicket(ctx, Session{}); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("invalid websocket session err=%v", err)
	}

	now := time.Now()
	s.ticketMu.Lock()
	s.wsTickets["expired"] = wsTicket{SessionID: "missing", JTI: "missing", ExpiresAt: now.Add(-time.Second)}
	s.wsTickets["orphan"] = wsTicket{SessionID: "missing", JTI: "missing", ExpiresAt: now.Add(time.Minute)}
	s.ticketMu.Unlock()
	if _, _, err := s.ConsumeWebSocketTicket(ctx, "expired"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("expired websocket ticket err=%v", err)
	}
	if _, _, err := s.ConsumeWebSocketTicket(ctx, "orphan"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("orphan websocket ticket err=%v", err)
	}

	s.mu.RLock()
	privateKey := append(ed25519.PrivateKey(nil), s.jwtPrivate...)
	s.mu.RUnlock()
	sign := func(subject, sessionID, jti string) string {
		t.Helper()
		claims := managementClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    managerIssuer,
				Subject:   subject,
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
				ID:        jti,
			},
			SessionID: sessionID,
		}
		token, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(privateKey)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	for name, token := range map[string]string{
		"bad subject":     sign("not-an-id", "session", "jti"),
		"missing session": sign("1", "", "jti"),
		"missing jti":     sign("1", "session", ""),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := s.AuthenticateBearer(ctx, token); !errors.Is(err, ErrSessionInvalid) {
				t.Fatalf("AuthenticateBearer err=%v", err)
			}
		})
	}

	wrongMethod := jwt.NewWithClaims(jwt.SigningMethodHS256, managementClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    managerIssuer,
			Subject:   "1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        "jti",
		},
		SessionID: "session",
	})
	wrongToken, err := wrongMethod.SignedString([]byte("not-the-ed25519-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateBearer(ctx, wrongToken); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("wrong signing method err=%v", err)
	}

	result, err := s.CreateBearerSession(ctx, admin, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := s.AuthenticateBearer(ctx, result.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.IssueWebSocketTicket(ctx, session); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("revoked session ticket err=%v", err)
	}
}

func TestPersistentSigningKeyPropagatesSchemaError(t *testing.T) {
	want := errors.New("schema unavailable")
	s := &Service{schemaErr: want}
	if err := s.UsePersistentSigningKey(t.TempDir()); !errors.Is(err, want) {
		t.Fatalf("UsePersistentSigningKey err=%v want=%v", err, want)
	}
}
