package auth

import (
	"context"
	"testing"
	"time"
)

func TestDisabledAndExpiredCredentials(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	u, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.ExecContext(ctx, "UPDATE users SET enabled=0 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "", ""); err == nil {
		t.Fatal("disabled user should not login")
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET enabled=1 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	token, _, _, err := s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE sessions SET expires_at=? WHERE token_hash=?", time.Now().Add(-time.Hour).Unix(), tokenHash(token)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionUserWithSession(ctx, token); err == nil {
		t.Fatal("expired session should fail")
	}

	token, _, _, err = s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET enabled=0 WHERE id=?", u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionUserWithSession(ctx, token); err == nil {
		t.Fatal("disabled session user should fail")
	}
}

func TestDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BootstrapRequired(ctx); err == nil {
		t.Fatal("expected bootstrap required DB error")
	}
	if _, err := s.Bootstrap(ctx, "admin", "correct-horse-battery"); err == nil {
		t.Fatal("expected bootstrap DB error")
	}
	if _, err := s.CreateUser(ctx, "operator", "correct-horse-battery"); err == nil {
		t.Fatal("expected create user DB error")
	}
	if _, err := s.ListUsers(ctx); err == nil {
		t.Fatal("expected list users DB error")
	}
	if _, err := s.UserByID(ctx, 1); err == nil {
		t.Fatal("expected user lookup DB error")
	}
	if err := s.SetUserEnabled(ctx, 1, false); err == nil {
		t.Fatal("expected user enabled DB error")
	}
	if err := s.DeleteUser(ctx, 1, 2); err == nil {
		t.Fatal("expected delete user DB error")
	}
	if err := s.ResetPassword(ctx, 1, "replacement-password"); err == nil {
		t.Fatal("expected reset password DB error")
	}
	if err := s.ChangePassword(ctx, 1, "current-password", "replacement-password", "session"); err == nil {
		t.Fatal("expected change password DB error")
	}
	if _, _, _, err := s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "", ""); err == nil {
		t.Fatal("expected login failure")
	}
	if err := s.Logout(ctx, "token"); err == nil {
		t.Fatal("expected logout DB error")
	}
	if _, _, err := s.SessionUserWithSession(ctx, "token"); err == nil {
		t.Fatal("expected session failure")
	}
	if err := s.ValidateCSRF(ctx, "session", "csrf"); err == nil {
		t.Fatal("expected csrf DB error")
	}
	if _, err := s.ListSessions(ctx, 1, "current"); err == nil {
		t.Fatal("expected list sessions DB error")
	}
	if err := s.RevokeSession(ctx, "session-id"); err == nil {
		t.Fatal("expected revoke session DB error")
	}
	if _, err := s.RevokeOtherSessions(ctx, 1, "current"); err == nil {
		t.Fatal("expected revoke other sessions DB error")
	}
	if _, err := s.RevokeAllSessions(ctx, 1); err == nil {
		t.Fatal("expected revoke all sessions DB error")
	}
	if _, _, err := s.CreateAPIKeyForUser(ctx, "key", 1); err == nil {
		t.Fatal("expected create key DB error")
	}
	if _, err := s.ListAPIKeys(ctx); err == nil {
		t.Fatal("expected list keys DB error")
	}
	if err := s.SetAPIKeyEnabled(ctx, "id", false); err == nil {
		t.Fatal("expected set key enabled DB error")
	}
	if err := s.UpdateAPIKey(ctx, "id", UpdateAPIKeyInput{}); err == nil {
		t.Fatal("expected update DB error")
	}
	if _, _, err := s.RotateAPIKey(ctx, "id"); err == nil {
		t.Fatal("expected rotate DB error")
	}
	if err := s.AuthenticateAPIKey(ctx, "sk-token"); err == nil {
		t.Fatal("expected authenticate DB error")
	}
	if _, err := s.ListServiceAccounts(ctx); err == nil {
		t.Fatal("expected list service accounts DB error")
	}
	if _, err := s.CreateServiceAccount(ctx, "bots", 1); err == nil {
		t.Fatal("expected create service account DB error")
	}
	if _, err := s.GetServiceAccount(ctx, "id"); err == nil {
		t.Fatal("expected get service account DB error")
	}
	name := "bots"
	enabled := true
	if err := s.UpdateServiceAccount(ctx, "id", &name, &enabled); err == nil {
		t.Fatal("expected update service account DB error")
	}
	if err := s.DeleteServiceAccount(ctx, "id"); err == nil {
		t.Fatal("expected delete service account DB error")
	}
	if _, err := s.ListAPIKeysForServiceAccount(ctx, "id"); err == nil {
		t.Fatal("expected list SA keys DB error")
	}
}

func TestVerifyPasswordRejectsUnsafeParameters(t *testing.T) {
	cases := []string{
		"argon2id$v=19$m=65536,t=0,p=2$c2FsdA$aGFzaA",
		"argon2id$v=19$m=65536,t=3,p=0$c2FsdA$aGFzaA",
		"argon2id$v=19$m=1,t=3,p=2$c2FsdA$aGFzaA",
		"argon2id$v=19$m=65536,t=3,p=2$$aGFzaA",
		"argon2id$v=19$m=65536,t=3,p=2$c2FsdA$",
		"argon2id$v=19$m=65536,t=3,p=2$%%%$aGFzaA",
		"argon2id$v=18$m=65536,t=3,p=2$c2FsdA$aGFzaA",
	}
	for _, encoded := range cases {
		if verifyPassword("password", encoded) {
			t.Fatalf("unsafe hash verified: %q", encoded)
		}
	}
}
