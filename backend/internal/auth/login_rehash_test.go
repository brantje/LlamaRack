package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

func legacyPasswordHash(password string) string {
	memory := uint32(argonMemory / 2)
	minimum := uint32(8) * uint32(argonThreads)
	if memory < minimum {
		memory = minimum
	}
	salt := []byte("0123456789abcdef")
	hash := argon2.IDKey([]byte(password), salt, argonTime, memory, argonThreads, argonKeyLength)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", memory, argonTime, argonThreads, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash))
}

func TestBearerAndLegacyLoginRehashOldPasswordParameters(t *testing.T) {
	ctx := t.Context()
	s := testService(t)
	user, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	legacy := legacyPasswordHash("correct-horse-battery")
	if !passwordNeedsRehash(legacy) || !verifyPassword("correct-horse-battery", legacy) {
		t.Fatal("legacy hash fixture must verify and require rehash")
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET password_hash=?,last_login_at=? WHERE id=?", legacy, 123, user.ID); err != nil {
		t.Fatal(err)
	}

	bearer, err := s.LoginBearerWithMetadata(ctx, " admin ", "correct-horse-battery", " 192.0.2.5 ", "agent")
	if err != nil {
		t.Fatal(err)
	}
	if bearer.AccessToken == "" || bearer.User.LastLoginAt == nil {
		t.Fatalf("bearer login result=%+v", bearer)
	}
	var current string
	if err := s.db.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=?", user.ID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if passwordNeedsRehash(current) {
		t.Fatal("bearer login did not upgrade password hash")
	}

	if _, err := s.db.ExecContext(ctx, "UPDATE users SET password_hash=? WHERE id=?", legacy, user.ID); err != nil {
		t.Fatal(err)
	}
	token, csrf, loggedIn, err := s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "192.0.2.6", "legacy-agent")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || csrf == "" || loggedIn.LastLoginAt == nil {
		t.Fatalf("legacy login token=%q csrf=%q user=%+v", token, csrf, loggedIn)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT password_hash FROM users WHERE id=?", user.ID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if passwordNeedsRehash(current) {
		t.Fatal("legacy login did not upgrade password hash")
	}
}

func TestBearerAdditionalValidationEdges(t *testing.T) {
	s := testService(t)
	if err := s.UsePersistentSigningKey(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	s.schemaErr = errors.New("schema unavailable")
	if err := s.UsePersistentSigningKey(t.TempDir()); err == nil {
		t.Fatal("schema error should prevent persistent key setup")
	}
	s.schemaErr = nil

	if _, _, err := s.AuthenticateBearer(t.Context(), " "); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("blank bearer err=%v", err)
	}
	wrongMethod, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": managerIssuer}).SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.parseManagementToken(wrongMethod); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("wrong signing method err=%v", err)
	}
	if _, _, err := s.IssueWebSocketTicket(t.Context(), Session{}); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("invalid websocket session err=%v", err)
	}
	if _, _, err := s.ConsumeWebSocketTicket(t.Context(), " "); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("blank websocket ticket err=%v", err)
	}
}
