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
	if u.Username != "admin" || !u.Enabled || u.ID == 0 || u.CreatedAt == 0 {
		t.Fatalf("unexpected user: %+v", u)
	}
	required, _ = s.BootstrapRequired(ctx)
	if required {
		t.Fatal("bootstrap should be complete")
	}
	if _, err := s.Bootstrap(ctx, "other", "correct-horse-battery"); err == nil {
		t.Fatal("expected duplicate bootstrap error")
	}
	if _, _, err := s.Login(ctx, "admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}

	token, csrf, loggedIn, err := s.LoginWithMetadata(ctx, " admin ", "correct-horse-battery", "192.0.2.10", "phase10-test")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || csrf == "" || loggedIn.ID != u.ID || loggedIn.LastLoginAt == nil {
		t.Fatalf("login token/csrf/user invalid: %q %q %+v", token, csrf, loggedIn)
	}
	sessionUser, session, err := s.SessionUserWithSession(ctx, token)
	if err != nil || sessionUser.ID != u.ID || session.RemoteAddress != "192.0.2.10" || session.UserAgent != "phase10-test" || session.ID == "" {
		t.Fatalf("session user=%+v session=%+v err=%v", sessionUser, session, err)
	}
	if err := s.ValidateCSRF(ctx, token, csrf); err != nil {
		t.Fatalf("csrf should validate: %v", err)
	}
	if err := s.ValidateCSRF(ctx, token, "wrong"); !errors.Is(err, ErrCSRFInvalid) {
		t.Fatalf("expected invalid csrf, got %v", err)
	}
	if _, err := s.SessionUser(ctx, "bogus"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("expected invalid session")
	}
	if err := s.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("session should be revoked")
	}
}

func TestUserAdministrationSafeguardsAndPasswords(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserEnabled(ctx, admin.ID, false); !errors.Is(err, ErrLastEnabledUser) {
		t.Fatalf("last enabled disable=%v", err)
	}

	other, err := s.CreateUser(ctx, "operator", "another-correct-password")
	if err != nil {
		t.Fatal(err)
	}
	users, err := s.ListUsers(ctx)
	if err != nil || len(users) != 2 {
		t.Fatalf("users=%+v err=%v", users, err)
	}
	if _, err := s.UserByID(ctx, other.ID); err != nil {
		t.Fatal(err)
	}

	adminToken, _, adminLoggedIn, err := s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, adminSession, err := s.SessionUserWithSession(ctx, adminToken)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, _, _, err := s.LoginWithMetadata(ctx, "operator", "another-correct-password", "", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetUserEnabled(ctx, other.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, otherToken); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("disabled user sessions should be revoked")
	}
	if err := s.SetUserEnabled(ctx, other.ID, true); err != nil {
		t.Fatal(err)
	}

	otherToken, _, _, err = s.LoginWithMetadata(ctx, "operator", "another-correct-password", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPassword(ctx, other.ID, "reset-password-long"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, otherToken); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("password reset should revoke target sessions")
	}
	if _, _, err := s.Login(ctx, "operator", "another-correct-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("old password should fail")
	}

	secondAdminToken, _, _, err := s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "", "second")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(ctx, adminLoggedIn.ID, "wrong", "new-correct-password", adminSession.ID); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password=%v", err)
	}
	if err := s.ChangePassword(ctx, adminLoggedIn.ID, "correct-horse-battery", "new-correct-password", adminSession.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, adminToken); err != nil {
		t.Fatalf("current session should remain: %v", err)
	}
	if _, err := s.SessionUser(ctx, secondAdminToken); !errors.Is(err, ErrSessionInvalid) {
		t.Fatal("other session should be revoked")
	}

	sessions, err := s.ListSessions(ctx, admin.ID, adminSession.ID)
	if err != nil || len(sessions) != 1 || !sessions[0].Current {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	if _, err := s.RevokeOtherSessions(ctx, admin.ID, adminSession.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing revoke=%v", err)
	}
	if _, err := s.RevokeAllSessions(ctx, admin.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, admin.ID, admin.ID); !errors.Is(err, ErrSelfDelete) {
		t.Fatalf("self delete=%v", err)
	}
	if err := s.SetUserEnabled(ctx, other.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, admin.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, 999, admin.ID); !errors.Is(err, ErrLastEnabledUser) {
		t.Fatalf("last enabled delete=%v", err)
	}
}

func TestAPIKeyLifecycleAndRotation(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, ""); err == nil {
		t.Fatal("expected missing key error")
	}
	if err := s.AuthenticateAPIKey(ctx, "invalid"); err == nil {
		t.Fatal("expected invalid key error")
	}

	key, secret, err := s.CreateAPIKeyForUser(ctx, "", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key.Name != "default" || !key.Enabled || key.ID == "" || key.Prefix == "" || secret == "" || key.CreatedByUserID == nil || *key.CreatedByUserID != admin.ID {
		t.Fatalf("unexpected key: %+v secret=%q", key, secret)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal("throttled repeat use should still authenticate")
	}
	keys, err := s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}

	if err := s.SetAPIKeyEnabled(ctx, key.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err == nil {
		t.Fatal("disabled key should fail")
	}
	if err := s.SetAPIKeyEnabled(ctx, key.ID, true); err != nil {
		t.Fatal(err)
	}

	replacement, replacementSecret, err := s.RotateAPIKey(ctx, key.ID, admin.ID)
	if err != nil || replacement.ID == key.ID || replacementSecret == "" {
		t.Fatalf("replacement=%+v secret=%q err=%v", replacement, replacementSecret, err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err == nil {
		t.Fatal("rotated old key should fail immediately")
	}
	if err := s.AuthenticateAPIKey(ctx, replacementSecret); err != nil {
		t.Fatalf("replacement should authenticate: %v", err)
	}
	keys, err = s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 2 || keys[1].RevokedAt == nil {
		t.Fatalf("rotation history=%+v err=%v", keys, err)
	}
	if err := s.SetAPIKeyEnabled(ctx, key.ID, true); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked key cannot be enabled: %v", err)
	}
	if err := s.RevokeAPIKey(ctx, replacement.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, replacementSecret); err == nil {
		t.Fatal("revoked replacement should fail")
	}
	keys, err = s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 2 || keys[0].RevokedAt == nil {
		t.Fatalf("revoked history should remain: %+v err=%v", keys, err)
	}

	legacy, _, err := s.CreateAPIKey(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAPIKey(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAPIKey(ctx, legacy.ID); !errors.Is(err, sql.ErrNoRows) {
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
	if passwordNeedsRehash(hash) {
		t.Fatal("current hash should not need rehash")
	}
	for _, malformed := range []string{"", "argon2id$bad", "argon2id$v=19$m=x,t=y,p=z$bad$bad"} {
		if verifyPassword("password", malformed) {
			t.Fatalf("malformed hash verified: %q", malformed)
		}
		if !passwordNeedsRehash(malformed) {
			t.Fatalf("malformed hash should require rehash: %q", malformed)
		}
	}
	if got := tokenHash("abc"); got == "" || got == "abc" {
		t.Fatalf("unexpected token hash %q", got)
	}
	if token, err := randomToken(16); err != nil || token == "" {
		t.Fatalf("randomToken=%q err=%v", token, err)
	}
	if got := truncate("abcdef", 3); got != "abc" {
		t.Fatalf("truncate=%q", got)
	}
}
