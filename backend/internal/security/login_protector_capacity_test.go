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

func TestLoginProtectorPreservesPenalizedAddressesAtCapacity(t *testing.T) {
	now := time.Unix(600000, 0)
	protector := &LoginProtector{
		addresses:    map[string]loginAttempt{},
		maxAddresses: 2,
	}
	protector.addresses["192.0.2.1"] = loginAttempt{
		Failures:  loginAddressDelayAfter,
		UpdatedAt: now.Add(-2 * time.Second),
	}
	protector.addresses["192.0.2.2"] = loginAttempt{
		Failures:  1,
		UpdatedAt: now.Add(-time.Second),
	}

	protector.recordAddressFailure("192.0.2.3", now)

	if _, ok := protector.addresses["192.0.2.1"]; !ok {
		t.Fatal("penalized address was evicted")
	}
	if _, ok := protector.addresses["192.0.2.2"]; ok {
		t.Fatal("oldest safe address was not evicted")
	}
	if _, ok := protector.addresses["192.0.2.3"]; !ok {
		t.Fatal("new address was not tracked after safe eviction")
	}

	newAttempt := protector.addresses["192.0.2.3"]
	newAttempt.Failures = loginAddressDelayAfter
	protector.addresses["192.0.2.3"] = newAttempt
	protector.recordAddressFailure("192.0.2.4", now.Add(time.Second))

	if len(protector.addresses) != protector.maxAddresses {
		t.Fatalf("address count=%d, want %d", len(protector.addresses), protector.maxAddresses)
	}
	if _, ok := protector.addresses["192.0.2.4"]; ok {
		t.Fatal("new address should remain untracked when every entry is penalized")
	}
	for _, address := range []string{"192.0.2.1", "192.0.2.3"} {
		if delay, _ := loginAttemptPenalty(protector.addresses[address], now.Add(time.Second), loginAddressDelayAfter); delay == 0 {
			t.Fatalf("address %s lost its active penalty", address)
		}
	}
}
