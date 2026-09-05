package security

import (
	"context"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestLoginProtectorEvictsOldestUnlockedEntryAtCapacity(t *testing.T) {
	ctx := context.Background()
	store := testSecuritySettings(t)
	if _, err := store.Set(ctx, settings.LoginFailureThreshold, 99); err != nil {
		t.Fatal(err)
	}
	protector := NewLoginProtector(store)
	protector.maxItems = 2
	now := time.Unix(500000, 0)
	protector.now = func() time.Time { return now }

	protector.Failure(ctx, "oldest", "192.0.2.1")
	now = now.Add(time.Second)
	protector.Failure(ctx, "newer", "192.0.2.2")
	now = now.Add(time.Second)
	protector.Failure(ctx, "newest", "192.0.2.3")

	if len(protector.attempts) != protector.maxItems {
		t.Fatalf("attempt count=%d, want %d", len(protector.attempts), protector.maxItems)
	}
	if _, ok := protector.attempts[loginKey("oldest", "192.0.2.1")]; ok {
		t.Fatal("oldest unlocked entry was not evicted")
	}
	if _, ok := protector.attempts[loginKey("newer", "192.0.2.2")]; !ok {
		t.Fatal("newer unlocked entry should remain")
	}
	if _, ok := protector.attempts[loginKey("newest", "192.0.2.3")]; !ok {
		t.Fatal("new failure was not admitted after unlocked eviction")
	}
}
