package security

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestNetworkEdgeParsingAndTrust(t *testing.T) {
	ctx := context.Background()
	store := testSecuritySettings(t)
	network := NewNetwork(store)

	if got := remoteIP("not-an-ip"); got.IsValid() {
		t.Fatalf("invalid remote parsed as %v", got)
	}
	if got := remoteIP("[2001:db8::1]:443"); got != netip.MustParseAddr("2001:db8::1") {
		t.Fatalf("ipv6 remote=%v", got)
	}
	if forwardedProto(`for=1.2.3.4;proto=ftp`) != "" {
		t.Fatal("unsupported forwarded proto should be ignored")
	}
	if got := forwardedProto(`for=1.2.3.4;proto="HTTPS"`); got != "https" {
		t.Fatalf("forwarded proto=%q", got)
	}
	chain := forwardedFor(`for="[2001:db8::2]:443";proto=https, for=bad, for=198.51.100.7`)
	if len(chain) != 2 || chain[0] != netip.MustParseAddr("2001:db8::2") || chain[1] != netip.MustParseAddr("198.51.100.7") {
		t.Fatalf("forwarded chain=%v", chain)
	}

	if network.isTrustedPeer(ctx, netip.Addr{}) {
		t.Fatal("invalid peer must not be trusted")
	}
	if network.isTrustedPeer(ctx, netip.MustParseAddr("10.0.0.1")) {
		t.Fatal("peer must not be trusted without config")
	}
	if _, err := store.Set(ctx, settings.TrustedProxies, "garbage, ,10.0.0.1,192.168.0.0/16"); err != nil {
		t.Fatal(err)
	}
	if !network.isTrustedPeer(ctx, netip.MustParseAddr("10.0.0.1")) || !network.isTrustedPeer(ctx, netip.MustParseAddr("192.168.1.4")) {
		t.Fatal("configured address/CIDR should be trusted")
	}
	if network.isTrustedPeer(ctx, netip.MustParseAddr("203.0.113.1")) {
		t.Fatal("unconfigured peer should not be trusted")
	}

	r := httptest.NewRequest(http.MethodGet, "https://manager.local/", nil)
	r.RemoteAddr = "broken-remote"
	r.TLS = &tls.ConnectionState{}
	if got := network.EffectiveScheme(r); got != "https" {
		t.Fatalf("TLS scheme=%q", got)
	}
	if got := network.EffectiveRemoteAddress(r); got != "invalid IP" {
		t.Fatalf("invalid remote text=%q", got)
	}
	if network.OriginAllowed(r, "://broken") {
		t.Fatal("malformed origin should be rejected")
	}

	trusted := httptest.NewRequest(http.MethodGet, "http://manager.local/", nil)
	trusted.RemoteAddr = "10.0.0.1:1234"
	trusted.Header.Set("X-Forwarded-Proto", " HTTPS , http")
	trusted.Header.Set("X-Forwarded-For", "bad, 198.51.100.8")
	if got := network.EffectiveScheme(trusted); got != "https" {
		t.Fatalf("x-forwarded scheme=%q", got)
	}
	if got := network.EffectiveRemoteAddress(trusted); got != "198.51.100.8" {
		t.Fatalf("x-forwarded remote=%q", got)
	}
}

func TestLoginProtectorPrunesExpiredAndOldestEntries(t *testing.T) {
	store := testSecuritySettings(t)
	p := NewLoginProtector(store)
	p.maxItems = 2
	now := time.Unix(200000, 0)
	p.now = func() time.Time { return now }

	p.attempts["expired"] = loginAttempt{Failures: 1, UpdatedAt: now.Add(-25 * time.Hour)}
	p.attempts["locked"] = loginAttempt{Failures: 9, UpdatedAt: now.Add(-25 * time.Hour), LockedUntil: now.Add(time.Hour)}
	p.attempts["old"] = loginAttempt{Failures: 1, UpdatedAt: now.Add(-2 * time.Hour)}
	p.attempts["new"] = loginAttempt{Failures: 1, UpdatedAt: now.Add(-time.Hour)}
	p.pruneLocked(now)

	if _, ok := p.attempts["expired"]; ok {
		t.Fatal("expired unlocked entry was not pruned")
	}
	if _, ok := p.attempts["locked"]; !ok {
		t.Fatal("active lockout must not be evicted")
	}
	if _, ok := p.attempts["old"]; ok {
		t.Fatal("oldest unlocked entry should be removed by maxItems pruning")
	}
	if len(p.attempts) != 2 {
		t.Fatalf("attempt count=%d entries=%v", len(p.attempts), p.attempts)
	}
	if _, ok := p.attempts["new"]; !ok {
		t.Fatal("expected newest entry to remain")
	}

	p.attempts = map[string]loginAttempt{}
	p.addresses = map[string]loginAttempt{}
	ctx := context.Background()
	if _, err := store.Set(ctx, settings.LoginFailureThreshold, 99); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		p.Failure(ctx, "shift-cap", "192.0.2.55")
	}
	if delay, locked := p.BeforeAttempt(ctx, "shift-cap", "192.0.2.55"); locked || delay != 1600*time.Millisecond {
		t.Fatalf("capped delay=%v locked=%v", delay, locked)
	}
}

func TestLoginProtectorKeepsActiveLockoutAtCapacity(t *testing.T) {
	ctx := context.Background()
	store := testSecuritySettings(t)
	if _, err := store.Set(ctx, settings.LoginFailureThreshold, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Set(ctx, settings.LoginLockoutSeconds, 60); err != nil {
		t.Fatal(err)
	}
	p := NewLoginProtector(store)
	p.maxItems = 3
	now := time.Unix(300000, 0)
	p.now = func() time.Time { return now }

	if p.Failure(ctx, "target", "192.0.2.1") {
		t.Fatal("first target failure should not lock")
	}
	if !p.Failure(ctx, "target", "192.0.2.1") {
		t.Fatal("second target failure should lock")
	}
	for i := 0; i < 20; i++ {
		p.Failure(ctx, fmt.Sprintf("spray-%d", i), fmt.Sprintf("198.51.100.%d", i+1))
	}
	if len(p.attempts) > p.maxItems {
		t.Fatalf("attempt tracker exceeded bound: %d > %d", len(p.attempts), p.maxItems)
	}
	if delay, locked := p.BeforeAttempt(ctx, "target", "192.0.2.1"); !locked || delay != time.Minute {
		t.Fatalf("target lockout was displaced: delay=%v locked=%v", delay, locked)
	}
}

func TestLoginProtectorAggregatesFailuresByAddress(t *testing.T) {
	ctx := context.Background()
	store := testSecuritySettings(t)
	if _, err := store.Set(ctx, settings.LoginFailureThreshold, 99); err != nil {
		t.Fatal(err)
	}
	p := NewLoginProtector(store)
	p.maxAddresses = 2
	now := time.Unix(400000, 0)
	p.now = func() time.Time { return now }

	address := "192.0.2.10"
	for i := 0; i < loginAddressDelayAfter; i++ {
		p.Failure(ctx, fmt.Sprintf("user-%d", i), address)
	}
	if delay, locked := p.BeforeAttempt(ctx, "never-seen", address); locked || delay != 100*time.Millisecond {
		t.Fatalf("aggregate delay=%v locked=%v", delay, locked)
	}
	if delay, locked := p.BeforeAttempt(ctx, "never-seen", "192.0.2.11"); locked || delay != 0 {
		t.Fatalf("different address was throttled: delay=%v locked=%v", delay, locked)
	}

	p.Success("user-0", address)
	if delay, _ := p.BeforeAttempt(ctx, "another-user", address); delay == 0 {
		t.Fatal("successful account login must not clear aggregate address pressure")
	}

	p.Failure(ctx, "other-a", "192.0.2.20")
	p.Failure(ctx, "other-b", "192.0.2.21")
	if len(p.addresses) > p.maxAddresses {
		t.Fatalf("address tracker exceeded bound: %d > %d", len(p.addresses), p.maxAddresses)
	}

	now = now.Add(loginAddressTTL + time.Second)
	if delay, locked := p.BeforeAttempt(ctx, "fresh-user", address); locked || delay != 0 {
		t.Fatalf("expired aggregate pressure remained: delay=%v locked=%v", delay, locked)
	}
}

func TestSecurityHeadersSkipHSTSOnPlainHTTP(t *testing.T) {
	network := NewNetwork(testSecuritySettings(t))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://manager.local/", nil)
	Headers(network, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })).ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d", w.Code)
	}
	if got := w.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("unexpected HSTS=%q", got)
	}
}
