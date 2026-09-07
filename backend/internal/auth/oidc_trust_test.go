package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func newStrictOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "oidc-strict.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	managerSettings := settings.New(db, settings.Defaults{SessionLifetime: time.Hour, StartupTimeout: time.Minute, AlwaysOnReconcile: time.Second})
	authService := New(db, time.Hour)
	secrets := newMemoryOIDCSecrets()
	return &oidcFixture{auth: authService, settings: managerSettings, secrets: secrets, manager: NewOIDCManager(authService, managerSettings, secrets)}
}

func TestOIDCRequiresHTTPSByDefault(t *testing.T) {
	f := newStrictOIDCFixture(t)
	secret := "client-secret"
	_, err := f.manager.CreateProvider(t.Context(), OIDCProviderInput{
		Name: "HTTP", Enabled: true, Issuer: "http://idp.example", ClientID: "client", ClientSecret: &secret,
	})
	if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("http issuer err=%v", err)
	}
}

func TestOIDCRejectsOffOriginDiscoveryAndManualToken(t *testing.T) {
	f := newStrictOIDCFixture(t)
	secret := "client-secret"
	_, err := f.manager.CreateProvider(t.Context(), OIDCProviderInput{
		Name: "Evil discovery", Enabled: true, Issuer: "https://accounts.example", DiscoveryURL: "https://evil.example/.well-known/openid-configuration",
		ClientID: "client", ClientSecret: &secret,
	})
	if err == nil || !strings.Contains(err.Error(), "discovery URL must be on the issuer origin") {
		t.Fatalf("off-origin discovery err=%v", err)
	}
	_, err = f.manager.CreateProvider(t.Context(), OIDCProviderInput{
		Name: "Evil token", Enabled: true, Issuer: "https://accounts.example", ClientID: "client", ClientSecret: &secret,
		AuthorizationEndpoint: "https://accounts.example/authorize",
		TokenEndpoint:         "https://evil.example/steal",
		JWKSURL:               "https://accounts.example/jwks",
	})
	if err == nil || !strings.Contains(err.Error(), "token endpoint host is not trusted") {
		t.Fatalf("manual off-origin token err=%v", err)
	}
}

func TestOIDCDiscoveredOffOriginPublicTokenIsTrusted(t *testing.T) {
	f := newOIDCFixture(t)
	tokenHits := 0
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHits++
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{"kty": "RSA"}}})
	}))
	defer tokenServer.Close()
	var issuer *httptest.Server
	issuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 issuer.URL,
			"authorization_endpoint": issuer.URL + "/authorize",
			"token_endpoint":         tokenServer.URL + "/token",
			"jwks_uri":               tokenServer.URL + "/jwks",
		})
	}))
	defer issuer.Close()

	secret := "client-secret"
	provider, err := f.manager.CreateProvider(t.Context(), OIDCProviderInput{
		Name: "Google-shaped", Enabled: true, Issuer: issuer.URL, ClientID: "client", ClientSecret: &secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.TestProvider(t.Context(), provider.ID); err != nil {
		t.Fatalf("discovered off-origin endpoints should be trusted: %v", err)
	}
	if tokenHits == 0 {
		t.Fatal("expected JWKS fetch against discovered off-origin host")
	}
}

func TestOIDCAllowHTTPWithoutPrivateHostsStillRejectsLoopbackDial(t *testing.T) {
	f := newStrictOIDCFixture(t)
	if _, err := f.settings.Set(t.Context(), settings.OIDCAllowHTTP, true); err != nil {
		t.Fatal(err)
	}
	idp := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer idp.Close()
	secret := "client-secret"
	provider, err := f.manager.CreateProvider(t.Context(), OIDCProviderInput{
		Name: "Loopback", Enabled: true, Issuer: idp.URL, ClientID: "client", ClientSecret: &secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.TestProvider(t.Context(), provider.ID); err == nil || !strings.Contains(err.Error(), "OIDC destination is not allowed") {
		t.Fatalf("allow_http must not permit loopback dial err=%v", err)
	}
}

func TestOIDCAllowedHostsPermitsPrivateIssuerWithoutHTTP(t *testing.T) {
	f := newStrictOIDCFixture(t)
	if _, err := f.settings.Set(t.Context(), settings.OIDCAllowedHosts, "192.168.10.5"); err != nil {
		t.Fatal(err)
	}
	secret := "client-secret"
	_, err := f.manager.CreateProvider(t.Context(), OIDCProviderInput{
		Name: "Authentik", Enabled: true, Issuer: "http://192.168.10.5:9000", ClientID: "client", ClientSecret: &secret,
	})
	if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("private allowlist must not enable HTTP err=%v", err)
	}
	if _, err := f.manager.CreateProvider(t.Context(), OIDCProviderInput{
		Name: "Authentik TLS", Enabled: true, Issuer: "https://192.168.10.5:9000", ClientID: "client", ClientSecret: &secret,
		AuthorizationEndpoint: "https://192.168.10.5:9000/authorize",
		TokenEndpoint:         "https://192.168.10.5:9000/token",
		JWKSURL:               "https://192.168.10.5:9000/jwks",
	}); err != nil {
		t.Fatalf("allowlisted HTTPS private issuer should save: %v", err)
	}
}
