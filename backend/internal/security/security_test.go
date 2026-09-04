package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func testSecuritySettings(t *testing.T) *settings.Service {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "security.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return settings.New(db, settings.Defaults{SessionLifetime: time.Hour, AllowedOrigins: "http://localhost:3000", StartupTimeout: time.Minute, AlwaysOnReconcile: time.Second})
}

func TestNetworkTrustsForwardingOnlyFromConfiguredProxy(t *testing.T) {
	ctx := context.Background()
	s := testSecuritySettings(t)
	if _, err := s.Set(ctx, settings.TrustedProxies, "10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	network := NewNetwork(s)

	r := httptest.NewRequest(http.MethodGet, "http://manager.local/api", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	r.Host = "manager.local"
	r.Header.Set("X-Forwarded-For", "198.51.100.20, 10.0.0.3")
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := network.EffectiveRemoteAddress(r); got != "198.51.100.20" {
		t.Fatalf("remote=%q", got)
	}
	if got := network.EffectiveScheme(r); got != "https" {
		t.Fatalf("scheme=%q", got)
	}
	if !network.IsSecure(r) {
		t.Fatal("expected secure request")
	}

	untrusted := httptest.NewRequest(http.MethodGet, "http://manager.local/api", nil)
	untrusted.RemoteAddr = "203.0.113.10:4444"
	untrusted.Header.Set("X-Forwarded-For", "198.51.100.20")
	untrusted.Header.Set("X-Forwarded-Proto", "https")
	if got := network.EffectiveRemoteAddress(untrusted); got != "203.0.113.10" {
		t.Fatalf("untrusted remote=%q", got)
	}
	if got := network.EffectiveScheme(untrusted); got != "http" {
		t.Fatalf("untrusted scheme=%q", got)
	}
}

func TestRequestForwardingDiagnostics(t *testing.T) {
	ctx := context.Background()
	s := testSecuritySettings(t)
	_, _ = s.Set(ctx, settings.TrustedProxies, "10.0.0.0/8")
	network := NewNetwork(s)

	direct := httptest.NewRequest(http.MethodGet, "http://manager.local/api/v1/system", nil)
	direct.RemoteAddr = "203.0.113.9:5555"
	got := network.RequestForwardingDiagnostics(direct)
	if got.PeerAddress != "203.0.113.9" || got.PeerTrusted || len(got.ForwardedHeader) != 0 || len(got.XForwardedFor) != 0 || got.EffectiveRemoteAddress != "203.0.113.9" {
		t.Fatalf("direct diagnostics=%+v", got)
	}

	proxied := httptest.NewRequest(http.MethodGet, "http://manager.local/api/v1/system", nil)
	proxied.RemoteAddr = "10.0.0.2:1234"
	proxied.Header.Set("Forwarded", `for=198.51.100.8;proto=https, for=10.0.0.1`)
	proxied.Header.Set("X-Forwarded-For", "198.51.100.20, 10.0.0.3")
	got = network.RequestForwardingDiagnostics(proxied)
	if got.PeerAddress != "10.0.0.2" || !got.PeerTrusted {
		t.Fatalf("peer diagnostics=%+v", got)
	}
	if len(got.ForwardedHeader) != 2 || got.ForwardedHeader[0] != "198.51.100.8" || got.ForwardedHeader[1] != "10.0.0.1" {
		t.Fatalf("forwarded header=%v", got.ForwardedHeader)
	}
	if len(got.XForwardedFor) != 2 || got.XForwardedFor[0] != "198.51.100.20" || got.XForwardedFor[1] != "10.0.0.3" {
		t.Fatalf("x-forwarded-for=%v", got.XForwardedFor)
	}
	if got.EffectiveRemoteAddress != "198.51.100.8" {
		t.Fatalf("effective remote=%q", got.EffectiveRemoteAddress)
	}
}

func TestForwardedHeaderExternalURLAndOrigins(t *testing.T) {
	ctx := context.Background()
	s := testSecuritySettings(t)
	_, _ = s.Set(ctx, settings.TrustedProxies, "10.0.0.1")
	network := NewNetwork(s)
	r := httptest.NewRequest(http.MethodPost, "http://manager.local/api", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Host = "manager.local"
	r.Header.Set("Forwarded", `for=198.51.100.8;proto=https`)
	if got := network.EffectiveRemoteAddress(r); got != "198.51.100.8" {
		t.Fatalf("forwarded remote=%q", got)
	}
	if got := network.EffectiveScheme(r); got != "https" {
		t.Fatalf("forwarded scheme=%q", got)
	}
	if !network.OriginAllowed(r, "http://localhost:3000") {
		t.Fatal("configured origin should be allowed")
	}
	if network.OriginAllowed(r, "https://evil.example") {
		t.Fatal("foreign origin should be rejected")
	}
	if !network.OriginAllowed(r, "") {
		t.Fatal("empty origin should be allowed")
	}

	_, _ = s.Set(ctx, settings.ExternalURL, "https://manager.example.com")
	if got := network.EffectiveScheme(r); got != "https" {
		t.Fatalf("external scheme=%q", got)
	}
}

func TestLoginProtectorEscalatesAndClears(t *testing.T) {
	ctx := context.Background()
	s := testSecuritySettings(t)
	_, _ = s.Set(ctx, settings.LoginFailureThreshold, 3)
	_, _ = s.Set(ctx, settings.LoginLockoutSeconds, 30)
	p := NewLoginProtector(s)
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }
	if delay, locked := p.BeforeAttempt(ctx, " Admin ", "192.0.2.1"); delay != 0 || locked {
		t.Fatalf("initial delay=%v locked=%v", delay, locked)
	}
	if p.Failure(ctx, "admin", "192.0.2.1") {
		t.Fatal("first failure should not lock")
	}
	if p.Failure(ctx, "admin", "192.0.2.1") {
		t.Fatal("second failure should not lock")
	}
	if delay, locked := p.BeforeAttempt(ctx, "admin", "192.0.2.1"); delay <= 0 || locked {
		t.Fatalf("expected escalating delay, got %v locked=%v", delay, locked)
	}
	if !p.Failure(ctx, "admin", "192.0.2.1") {
		t.Fatal("threshold failure should lock")
	}
	if delay, locked := p.BeforeAttempt(ctx, "admin", "192.0.2.1"); !locked || delay != 30*time.Second {
		t.Fatalf("lock delay=%v locked=%v", delay, locked)
	}
	p.Success("admin", "192.0.2.1")
	if delay, locked := p.BeforeAttempt(ctx, "admin", "192.0.2.1"); delay != 0 || locked {
		t.Fatal("success should clear limiter")
	}

	_, _ = s.Set(ctx, settings.LoginProtectionEnabled, false)
	if p.Failure(ctx, "admin", "x") {
		t.Fatal("disabled protection should not lock")
	}
}

func TestRedactAndSecurityHeaders(t *testing.T) {
	value := Redact("Authorization: Bearer abc.def hf_abcdefghijkl sk-lcm-abcdefghijkl lcm_session=secret; lcm_csrf=token llamarack_oidc_state=oidc-secret")
	if strings.Contains(value, "abc.def") || strings.Contains(value, "hf_abcdefghijkl") || strings.Contains(value, "sk-lcm-abcdefghijkl") || strings.Contains(value, "secret") || strings.Contains(value, "oidc-secret") {
		t.Fatalf("redaction leaked secret: %s", value)
	}

	s := testSecuritySettings(t)
	_, _ = s.Set(context.Background(), settings.ExternalURL, "https://manager.example.com")
	network := NewNetwork(s)
	recorder := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://manager.local/", nil)
	Headers(network, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(recorder, r)
	for _, header := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy", "Strict-Transport-Security"} {
		if recorder.Header().Get(header) == "" {
			t.Fatalf("missing %s", header)
		}
	}
}
