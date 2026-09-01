package auth

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestParseManagementTokenRejectsOtherIssuers(t *testing.T) {
	ctx := t.Context()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.CreateBearerSession(ctx, admin, "", "")
	if err != nil {
		t.Fatal(err)
	}
	claims, err := s.parseManagementToken(result.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != managerIssuer {
		t.Fatalf("issued issuer = %q", claims.Issuer)
	}
	if _, _, err := s.AuthenticateBearer(ctx, result.AccessToken); err != nil {
		t.Fatalf("canonical issuer authenticate err=%v", err)
	}

	s.mu.RLock()
	privateKey := append(ed25519.PrivateKey(nil), s.jwtPrivate...)
	s.mu.RUnlock()
	foreignToken, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, managementClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "llamacpp-manager",
			Subject:   claims.Subject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        claims.ID,
		},
		SessionID: claims.SessionID,
	}).SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AuthenticateBearer(ctx, foreignToken); err == nil {
		t.Fatal("previous product issuer must be rejected")
	}
}
