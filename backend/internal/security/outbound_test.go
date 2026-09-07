package security

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func testOutboundSettings(t *testing.T) *settings.Service {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "oidc-outbound.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return settings.New(db, settings.Defaults{SessionLifetime: time.Hour, StartupTimeout: time.Minute, AlwaysOnReconcile: time.Second})
}

type mapResolver map[string][]string

func (r mapResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	values, ok := r[strings.ToLower(host)]
	if !ok {
		return nil, errors.New("no such host")
	}
	out := make([]net.IPAddr, 0, len(values))
	for _, value := range values {
		ip := net.ParseIP(value)
		if ip == nil {
			continue
		}
		out = append(out, net.IPAddr{IP: ip})
	}
	if len(out) == 0 {
		return nil, errors.New("no such host")
	}
	return out, nil
}

type recordingDialer struct {
	addrs []string
}

func (d *recordingDialer) DialContext(_ context.Context, _, addr string) (net.Conn, error) {
	d.addrs = append(d.addrs, addr)
	return nil, errors.New("dial disabled")
}

func newPolicy(t *testing.T, s *settings.Service, resolver mapResolver, dialer *recordingDialer) *oidcOutboundPolicy {
	t.Helper()
	return &oidcOutboundPolicy{settings: s, resolver: resolver, dialer: dialer}
}

func TestParseAndMatchOIDCAllowedHosts(t *testing.T) {
	got := ParseOIDCAllowedHosts(" Auth.LAN, 192.168.1.10; [::1]:443  127.0.0.1 ")
	if strings.Join(got, ",") != "auth.lan,192.168.1.10,::1,127.0.0.1" {
		t.Fatalf("hosts=%v", got)
	}
	if !HostAllowed("AUTH.lan", got) || !HostAllowed("[::1]", got) || HostAllowed("evil.example", got) {
		t.Fatalf("allowlist matching failed for %v", got)
	}
}

func TestBlockedOIDCAddrClasses(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "::1", "10.0.0.8", "192.168.1.20", "172.16.5.1", "169.254.169.254", "224.0.0.1", "0.0.0.0", "100.64.1.2", "fc00::1", "fe80::1", "::ffff:10.1.2.3", "2002:c0a8:101::1", "2001:0:53aa:64c::1"} {
		ip := netip.MustParseAddr(value)
		if !BlockedOIDCAddr(ip) {
			t.Fatalf("%s should be blocked", value)
		}
	}
	for _, value := range []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888"} {
		ip := netip.MustParseAddr(value)
		if BlockedOIDCAddr(ip) {
			t.Fatalf("%s should be public", value)
		}
	}
}

func TestValidateOIDCURL(t *testing.T) {
	if _, err := ValidateOIDCURL("https://idp.example/application/o/app", false); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateOIDCURL("http://idp.example/application/o/app", false); !errors.Is(err, errOIDCHTTPS) {
		t.Fatalf("http without flag err=%v", err)
	}
	if _, err := ValidateOIDCURL("http://idp.example/application/o/app", true); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateOIDCURL("https://user:secret@idp.example/", false); !errors.Is(err, errOIDCCredentials) {
		t.Fatalf("userinfo err=%v", err)
	}
	if _, err := ValidateOIDCURL("file:///jwks", false); err == nil {
		t.Fatal("file URL should fail")
	}
}

func TestOIDCEndpointHostTrusted(t *testing.T) {
	issuer, _ := url.Parse("https://accounts.example/realms/app")
	same, _ := url.Parse("https://accounts.example/realms/app/protocol/openid-connect/token")
	discovered, _ := url.Parse("https://oauth.example/token")
	manual, _ := url.Parse("https://evil.example/steal")
	if !OIDCEndpointHostTrusted(same, issuer, nil, false) {
		t.Fatal("issuer origin should be trusted")
	}
	if !OIDCEndpointHostTrusted(discovered, issuer, nil, true) {
		t.Fatal("discovered public host should be trusted")
	}
	if OIDCEndpointHostTrusted(manual, issuer, nil, false) {
		t.Fatal("manual off-origin host should be rejected")
	}
	if !OIDCEndpointHostTrusted(manual, issuer, []string{"evil.example"}, false) {
		t.Fatal("allowlisted manual host should be trusted")
	}
}

func TestOIDCDialRejectsPrivateAndRebinding(t *testing.T) {
	s := testOutboundSettings(t)
	dialer := &recordingDialer{}
	policy := newPolicy(t, s, mapResolver{
		"idp.example":   {"8.8.8.8"},
		"evil.example":  {"127.0.0.1"},
		"mixed.example": {"8.8.8.8", "10.0.0.9"},
		"cgnat.example": {"100.64.0.10"},
	}, dialer)

	if _, err := policy.pickDialAddr(t.Context(), "idp.example", "443"); err != nil {
		t.Fatalf("public host err=%v", err)
	}
	if len(dialer.addrs) != 0 {
		t.Fatalf("pickDialAddr should not dial, got %v", dialer.addrs)
	}
	if _, err := policy.dialContext(t.Context(), "tcp", "idp.example:443"); !strings.Contains(err.Error(), "dial disabled") {
		t.Fatalf("public dial err=%v", err)
	}
	if len(dialer.addrs) != 1 || dialer.addrs[0] != "8.8.8.8:443" {
		t.Fatalf("dialed %v", dialer.addrs)
	}
	if _, err := policy.dialContext(t.Context(), "tcp", "evil.example:443"); !errors.Is(err, errOIDCDestination) {
		t.Fatalf("rebinding err=%v", err)
	}
	if _, err := policy.dialContext(t.Context(), "tcp", "cgnat.example:443"); !errors.Is(err, errOIDCDestination) {
		t.Fatalf("cgnat err=%v", err)
	}
	if _, err := policy.dialContext(t.Context(), "tcp", "127.0.0.1:80"); !errors.Is(err, errOIDCDestination) {
		t.Fatalf("loopback literal err=%v", err)
	}
	if _, err := policy.dialContext(t.Context(), "tcp", "mixed.example:443"); !strings.Contains(err.Error(), "dial disabled") {
		t.Fatalf("dual-homed public pick err=%v", err)
	}
	if dialer.addrs[len(dialer.addrs)-1] != "8.8.8.8:443" {
		t.Fatalf("dual-homed should dial public IP, got %v", dialer.addrs)
	}
}

func TestOIDCAllowHTTPDoesNotAllowLoopback(t *testing.T) {
	s := testOutboundSettings(t)
	if _, err := s.Set(t.Context(), settings.OIDCAllowHTTP, true); err != nil {
		t.Fatal(err)
	}
	client := NewOIDCClient(s)
	_, err := client.Get("http://127.0.0.1:9/")
	if err == nil || !strings.Contains(err.Error(), errOIDCDestination.Error()) {
		t.Fatalf("http loopback err=%v", err)
	}
}

func TestOIDCAllowedHostsPermitsPrivateWithoutHTTP(t *testing.T) {
	s := testOutboundSettings(t)
	if _, err := s.Set(t.Context(), settings.OIDCAllowedHosts, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateOIDCURL("http://127.0.0.1/", false); !errors.Is(err, errOIDCHTTPS) {
		t.Fatalf("allowed host must not enable HTTP: %v", err)
	}
	dialer := &recordingDialer{}
	policy := newPolicy(t, s, mapResolver{"authentik.lan": {"192.168.1.50"}}, dialer)
	if _, err := policy.dialContext(t.Context(), "tcp", "127.0.0.1:443"); !strings.Contains(err.Error(), "dial disabled") {
		t.Fatalf("allowlisted loopback err=%v", err)
	}
	if _, err := policy.dialContext(t.Context(), "tcp", "authentik.lan:443"); !errors.Is(err, errOIDCDestination) {
		t.Fatalf("unlisted private hostname err=%v", err)
	}
	if _, err := s.Set(t.Context(), settings.OIDCAllowedHosts, "authentik.lan"); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.dialContext(t.Context(), "tcp", "authentik.lan:443"); !strings.Contains(err.Error(), "dial disabled") {
		t.Fatalf("allowlisted private hostname err=%v", err)
	}
	if dialer.addrs[len(dialer.addrs)-1] != "192.168.1.50:443" {
		t.Fatalf("dialed %v", dialer.addrs)
	}
}

func TestOIDCRedirectPolicy(t *testing.T) {
	s := testOutboundSettings(t)
	policy := newPolicy(t, s, nil, &recordingDialer{})
	origin := httptest.NewRequest(http.MethodGet, "https://idp.example/.well-known/openid-configuration", nil)
	same := httptest.NewRequest(http.MethodGet, "https://idp.example/openid-configuration", nil)
	if err := policy.checkRedirect(same, []*http.Request{origin}); err != nil {
		t.Fatalf("same-origin redirect err=%v", err)
	}
	cross := httptest.NewRequest(http.MethodGet, "https://evil.example/latest", nil)
	if err := policy.checkRedirect(cross, []*http.Request{origin}); err == nil || !errors.Is(err, errOIDCRedirect) {
		t.Fatalf("cross-origin discovery redirect err=%v", err)
	}
	token := httptest.NewRequest(http.MethodPost, "https://idp.example/token", nil)
	steal := httptest.NewRequest(http.MethodPost, "https://evil.example/steal", nil)
	if err := policy.checkRedirect(steal, []*http.Request{token}); err == nil || !errors.Is(err, errOIDCRedirect) {
		t.Fatalf("cross-host token redirect err=%v", err)
	}
	loopback := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/.well-known/openid-configuration", nil)
	if err := policy.checkRedirect(loopback, []*http.Request{origin}); err == nil {
		t.Fatalf("loopback redirect should fail, err=%v", err)
	}
	tooMany := make([]*http.Request, oidcMaxRedirects)
	for i := range tooMany {
		tooMany[i] = origin
	}
	if err := policy.checkRedirect(same, tooMany); err == nil || !strings.Contains(err.Error(), "too many OIDC redirects") {
		t.Fatalf("redirect limit err=%v", err)
	}
}

func TestOIDCOutboundEdgeBranches(t *testing.T) {
	if SameOrigin(nil, nil) || OIDCEndpointHostTrusted(nil, nil, nil, false) || HostAllowed("", nil) || HostAllowed("idp.example", nil) {
		t.Fatal("empty inputs should be untrusted")
	}
	if !BlockedOIDCAddr(netip.Addr{}) {
		t.Fatal("invalid IP should be blocked")
	}
	if _, err := ValidateOIDCURL("https://", false); err == nil {
		t.Fatal("empty host should fail")
	}
	if _, err := ValidateOIDCURL("http:opaque", false); err == nil {
		t.Fatal("opaque URL should fail")
	}
	s := testOutboundSettings(t)
	policy := &oidcOutboundPolicy{settings: nil}
	if _, err := policy.snapshot(t.Context()); !errors.Is(err, ErrOIDCDestination) {
		t.Fatalf("nil settings err=%v", err)
	}
	client := newOIDCClient(&oidcOutboundPolicy{settings: s})
	if client.Timeout != oidcClientTimeout {
		t.Fatalf("timeout=%s", client.Timeout)
	}
	if _, err := policy.dialContext(t.Context(), "unix", "/tmp/oidc.sock"); !errors.Is(err, ErrOIDCDestination) {
		t.Fatalf("unix dial err=%v", err)
	}
	next := httptest.NewRequest(http.MethodGet, "https://idp.example/next", nil)
	if err := newPolicy(t, s, nil, &recordingDialer{}).checkRedirect(next, nil); !errors.Is(err, ErrOIDCRedirect) {
		t.Fatalf("empty via err=%v", err)
	}
	if err := allowOIDCIP(netip.Addr{}, "idp.example", nil); !errors.Is(err, ErrOIDCDestination) {
		t.Fatalf("invalid allow IP err=%v", err)
	}
	missing := newPolicy(t, s, mapResolver{}, &recordingDialer{})
	if _, err := missing.pickDialAddr(t.Context(), "missing.example", "443"); err == nil {
		t.Fatal("missing host should fail lookup")
	}
	if _, err := missing.dialContext(t.Context(), "tcp", "not-an-address"); err == nil {
		t.Fatal("invalid dial addr should fail")
	}
}

func TestOIDCClientFollowsSameOriginRedirectsOnly(t *testing.T) {
	s := testOutboundSettings(t)
	if _, err := s.Set(t.Context(), settings.OIDCAllowHTTP, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(t.Context(), settings.OIDCAllowedHosts, "127.0.0.1,::1"); err != nil {
		t.Fatal(err)
	}
	internalHits := 0
	internal := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		internalHits++
	}))
	defer internal.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cross" {
			http.Redirect(w, r, internal.URL+"/secret", http.StatusFound)
			return
		}
		if r.URL.Path == "/same" {
			http.Redirect(w, r, "/ok", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer origin.Close()

	client := NewOIDCClient(s)
	resp, err := client.Get(origin.URL + "/same")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("same-origin body=%q", body)
	}
	_, err = client.Get(origin.URL + "/cross")
	if err == nil || !strings.Contains(err.Error(), errOIDCRedirect.Error()) {
		t.Fatalf("cross-origin redirect err=%v", err)
	}
	if internalHits != 0 {
		t.Fatalf("internal server hits=%d", internalHits)
	}
}

func TestOIDCSettingsDefaultToDeniedHTTP(t *testing.T) {
	s := testOutboundSettings(t)
	allowHTTP, err := s.Bool(t.Context(), settings.OIDCAllowHTTP)
	if err != nil || allowHTTP {
		t.Fatalf("allow http default=%v err=%v", allowHTTP, err)
	}
	hosts, err := s.String(t.Context(), settings.OIDCAllowedHosts)
	if err != nil || hosts != "" {
		t.Fatalf("allowed hosts default=%q err=%v", hosts, err)
	}
}
