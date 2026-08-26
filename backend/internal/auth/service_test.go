package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/database"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, time.Hour)
}

func TestBootstrapLoginSessionLogout(t *testing.T) {
	ctx := context.Background()
	s := testService(t)

	required, err := s.BootstrapRequired(ctx)
	if err != nil || !required {
		t.Fatalf("bootstrap required=%v err=%v", required, err)
	}
	if _, err := s.Bootstrap(ctx, "x", "0123456789"); err == nil {
		t.Fatal("expected short username error")
	}
	if _, err := s.Bootstrap(ctx, "admin", "short"); err == nil {
		t.Fatal("expected short password error")
	}

	u, err := s.Bootstrap(ctx, " admin ", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "admin" || !u.Enabled || u.ID == 0 {
		t.Fatalf("unexpected user: %+v", u)
	}
	required, _ = s.BootstrapRequired(ctx)
	if required {
		t.Fatal("bootstrap should be complete")
	}
	if _, err := s.Bootstrap(ctx, "other", "correct-horse-battery"); err == nil {
		t.Fatal("expected duplicate bootstrap error")
	}
	if _, _, err := s.Login(ctx, "admin", "wrong-password"); err == nil {
		t.Fatal("expected invalid credentials")
	}

	token, loggedIn, err := s.Login(ctx, " admin ", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || loggedIn.ID != u.ID {
		t.Fatalf("login token/user invalid: %q %+v", token, loggedIn)
	}
	sessionUser, err := s.SessionUser(ctx, token)
	if err != nil || sessionUser.ID != u.ID {
		t.Fatalf("session user=%+v err=%v", sessionUser, err)
	}
	if _, err := s.SessionUser(ctx, "bogus"); err == nil {
		t.Fatal("expected invalid session")
	}
	if err := s.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, token); err == nil {
		t.Fatal("session should be revoked")
	}
}

func TestAPIKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	if err := s.AuthenticateAPIKey(ctx, ""); err == nil {
		t.Fatal("expected missing key error")
	}
	if err := s.AuthenticateAPIKey(ctx, "invalid"); err == nil {
		t.Fatal("expected invalid key error")
	}

	key, secret, err := s.CreateAPIKey(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if key.Name != "default" || !key.Enabled || key.ID == "" || key.Prefix == "" || secret == "" {
		t.Fatalf("unexpected key: %+v secret=%q", key, secret)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal(err)
	}
	keys, err := s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
	if keys[0].LastUsedAt == nil {
		t.Fatal("last_used_at should be populated")
	}

	if err := s.SetAPIKeyEnabled(ctx, key.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err == nil {
		t.Fatal("disabled key should fail")
	}
	keys, err = s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].Enabled {
		t.Fatalf("expected disabled key: %+v err=%v", keys, err)
	}

	if err := s.SetAPIKeyEnabled(ctx, key.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatalf("re-enabled key should authenticate: %v", err)
	}
	if err := s.SetAPIKeyEnabled(ctx, "missing", true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing enable error=%v", err)
	}

	if err := s.RevokeAPIKey(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err == nil {
		t.Fatal("deleted key should fail")
	}
	keys, err = s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 0 {
		t.Fatalf("revoked key should be removed: %+v err=%v", keys, err)
	}
	if err := s.DeleteAPIKey(ctx, key.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing delete error=%v", err)
	}
}

func TestPasswordAndTokenHelpers(t *testing.T) {
	hash, err := hashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("correct-horse-battery", hash) || verifyPassword("wrong", hash) {
		t.Fatal("password verification mismatch")
	}
	for _, malformed := range []string{"", "argon2id$bad", "argon2id$v=19$m=x,t=y,p=z$bad$bad"} {
		if verifyPassword("password", malformed) {
			t.Fatalf("malformed hash verified: %q", malformed)
		}
	}
	if got := tokenHash("abc"); got == "" || got == "abc" {
		t.Fatalf("unexpected token hash %q", got)
	}
	if token, err := randomToken(16); err != nil || token == "" {
		t.Fatalf("randomToken=%q err=%v", token, err)
	}
}
