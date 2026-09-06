package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAPIKeyCacheHitAvoidsSQLite(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := s.CreateAPIKeyForUser(ctx, "cached", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.AuthenticateAPIKeyInfo(ctx, secret)
	if err != nil || first.LastUsedAt == nil {
		t.Fatalf("first auth=%+v err=%v", first, err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	blockedCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	second, err := s.AuthenticateAPIKeyInfo(blockedCtx, secret)
	if err != nil {
		t.Fatalf("warm cache hit touched SQLite: %v", err)
	}
	if second.ID != first.ID || second.LastUsedAt == nil {
		t.Fatalf("second auth=%+v", second)
	}
}

func TestAPIKeyCacheInvalidatesOnKeyAndOwnerChanges(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	operator, err := s.CreateUser(ctx, "operator", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	key, secret, err := s.CreateAPIKeyForUser(ctx, "cached", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAPIKeyEnabled(ctx, key.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Fatalf("disabled cached key auth=%v", err)
	}
	if err := s.SetAPIKeyEnabled(ctx, key.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserEnabled(ctx, admin.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, secret); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Fatalf("disabled owner cached key auth=%v", err)
	}

	account, err := s.CreateServiceAccount(ctx, "automation", operator.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, serviceSecret, err := s.CreateAPIKey(ctx, CreateAPIKeyInput{Name: "service", OwnerServiceAccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, serviceSecret); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if err := s.UpdateServiceAccount(ctx, account.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, serviceSecret); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Fatalf("disabled service account cached key auth=%v", err)
	}
}

func TestAPIKeyCacheInvalidatesOnRotation(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	key, oldSecret, err := s.CreateAPIKeyForUser(ctx, "rotate", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, oldSecret); err != nil {
		t.Fatal(err)
	}
	_, newSecret, err := s.RotateAPIKey(ctx, key.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateAPIKey(ctx, oldSecret); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Fatalf("old secret survived rotation: %v", err)
	}
	if err := s.AuthenticateAPIKey(ctx, newSecret); err != nil {
		t.Fatalf("new secret auth=%v", err)
	}
}

func TestAPIKeyCacheUnknownTokenIsNotRemembered(t *testing.T) {
	s := testService(t)
	if err := s.AuthenticateAPIKey(context.Background(), "sk-unknown"); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Fatalf("unknown auth=%v", err)
	}
	state := apiKeyCacheFor(s)
	state.mu.RLock()
	count := len(state.byHash)
	state.mu.RUnlock()
	if count != 0 {
		t.Fatalf("unknown token cached: %d", count)
	}
}

func TestAPIKeyUseWriteSeedsFromPersistedTimestamp(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	admin, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	key, secret, err := s.CreateAPIKeyForUser(ctx, "seeded", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	persisted := time.Now().Add(-5 * time.Second).Unix()
	if _, err := s.db.ExecContext(ctx, "UPDATE api_keys SET last_used_at=? WHERE id=?", persisted, key.ID); err != nil {
		t.Fatal(err)
	}
	reloaded := New(s.db, time.Hour)
	item, err := reloaded.AuthenticateAPIKeyInfo(ctx, secret)
	if err != nil {
		t.Fatal(err)
	}
	if item.LastUsedAt == nil || *item.LastUsedAt < persisted {
		t.Fatalf("in-memory last_used_at=%v", item.LastUsedAt)
	}
	var stored int64
	if err := s.db.QueryRowContext(ctx, "SELECT last_used_at FROM api_keys WHERE id=?", key.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != persisted {
		t.Fatalf("restart rewrote recent usage: got=%d want=%d", stored, persisted)
	}
}
