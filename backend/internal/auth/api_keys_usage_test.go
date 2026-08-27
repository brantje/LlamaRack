package auth

import (
	"context"
	"testing"
	"time"
)

func TestAPIKeyLastUsedWriteIsCoalescedAcrossRestart(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	user, err := s.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	key, secret, err := s.CreateAPIKeyForUser(ctx, "usage", user.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal(err)
	}
	var first int64
	if err := s.db.QueryRowContext(ctx, "SELECT last_used_at FROM api_keys WHERE id=?", key.ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	if first == 0 {
		t.Fatal("expected first authentication to persist last_used_at")
	}

	restarted := New(s.db, time.Hour)
	if err := restarted.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal(err)
	}
	var second int64
	if err := s.db.QueryRowContext(ctx, "SELECT last_used_at FROM api_keys WHERE id=?", key.ID).Scan(&second); err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("last_used_at changed inside coalescing window after restart: first=%d second=%d", first, second)
	}

	now := time.Now()
	if !restarted.reserveAPIUseWrite(key.ID, now) {
		t.Fatal("expected first in-memory write reservation")
	}
	if restarted.reserveAPIUseWrite(key.ID, now.Add(time.Millisecond)) {
		t.Fatal("concurrent write inside coalescing window should be suppressed")
	}
	restarted.releaseAPIUseWrite(key.ID, now)
	if !restarted.reserveAPIUseWrite(key.ID, now.Add(2*time.Millisecond)) {
		t.Fatal("failed write release should allow immediate retry")
	}
	restarted.clearAPIUseWrite(key.ID)
	if !restarted.reserveAPIUseWrite(key.ID, now.Add(3*time.Millisecond)) {
		t.Fatal("cleared usage gate should allow a new reservation")
	}
}
