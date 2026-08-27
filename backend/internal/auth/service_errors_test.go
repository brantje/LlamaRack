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
	if err != nil { t.Fatal(err) }

	if _, err := s.db.ExecContext(ctx, "UPDATE users SET enabled=0 WHERE id=?", u.ID); err != nil { t.Fatal(err) }
	if _, _, _, err := s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "", ""); err == nil { t.Fatal("disabled user should not login") }
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET enabled=1 WHERE id=?", u.ID); err != nil { t.Fatal(err) }
	token, _, _, err := s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "", "")
	if err != nil { t.Fatal(err) }
	if _, err := s.db.ExecContext(ctx, "UPDATE sessions SET expires_at=? WHERE token_hash=?", time.Now().Add(-time.Hour).Unix(), tokenHash(token)); err != nil { t.Fatal(err) }
	if _, err := s.SessionUser(ctx, token); err == nil { t.Fatal("expired session should fail") }

	token, _, _, err = s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "", "")
	if err != nil { t.Fatal(err) }
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET enabled=0 WHERE id=?", u.ID); err != nil { t.Fatal(err) }
	if _, err := s.SessionUser(ctx, token); err == nil { t.Fatal("disabled session user should fail") }
}

func TestDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	if err := s.db.Close(); err != nil { t.Fatal(err) }
	if _, err := s.BootstrapRequired(ctx); err == nil { t.Fatal("expected bootstrap required DB error") }
	if _, err := s.Bootstrap(ctx, "admin", "correct-horse-battery"); err == nil { t.Fatal("expected bootstrap DB error") }
	if _, _, _, err := s.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "", ""); err == nil { t.Fatal("expected login failure") }
	if err := s.Logout(ctx, "token"); err == nil { t.Fatal("expected logout DB error") }
	if _, err := s.SessionUser(ctx, "token"); err == nil { t.Fatal("expected session failure") }
	if _, _, err := s.CreateAPIKeyForUser(ctx, "key", 1); err == nil { t.Fatal("expected create key DB error") }
	if _, err := s.ListAPIKeys(ctx); err == nil { t.Fatal("expected list keys DB error") }
	if err := s.RevokeAPIKey(ctx, "id"); err == nil { t.Fatal("expected revoke DB error") }
	if err := s.AuthenticateAPIKey(ctx, "token"); err == nil { t.Fatal("expected authenticate DB error") }
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
		if verifyPassword("password", encoded) { t.Fatalf("unsafe hash verified: %q", encoded) }
	}
}
