package auth

import (
	"context"
	"testing"
)

func TestBootstrapHandlesUserCountQueryFailure(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	if _, err := s.db.ExecContext(ctx, `DROP TABLE users`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bootstrap(ctx, "admin", "correct-horse-battery"); err == nil {
		t.Fatal("expected bootstrap user query error")
	}
}

func TestSetAPIKeyEnabledClosedDatabase(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAPIKeyEnabled(ctx, "missing", true); err == nil {
		t.Fatal("expected enable error on closed database")
	}
}

func TestListAPIKeysReturnsScanError(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_keys(id,name,prefix,token_hash,enabled,created_at) VALUES('bad','Bad','bad','hash',1,'not-a-number')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListAPIKeys(ctx); err == nil {
		t.Fatal("expected API key scan error")
	}
}

func TestVerifyPasswordAdditionalMalformedShapes(t *testing.T) {
	for _, encoded := range []string{
		"argon2id$v=19$m=65536,t=3$c2FsdA$aGFzaA",
		"argon2id$v=19$m=bad,t=3,p=2$c2FsdA$aGFzaA",
		"argon2id$v=19$m=65536,t=bad,p=2$c2FsdA$aGFzaA",
		"argon2id$v=19$m=65536,t=3,p=bad$c2FsdA$aGFzaA",
		"argon2id$v=19$m=65536,t=3,p=2$c2FsdA$%%%",
	} {
		if verifyPassword("password", encoded) {
			t.Fatalf("malformed hash verified: %q", encoded)
		}
	}
}
