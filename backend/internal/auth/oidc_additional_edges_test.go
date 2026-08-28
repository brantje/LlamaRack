package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOIDCProviderNetworkAndStartErrorBranches(t *testing.T) {
	f := newOIDCFixture(t)
	ctx := t.Context()

	badDiscovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			http.Error(w, "unavailable", http.StatusBadGateway)
		case "/invalid-json":
			_, _ = w.Write([]byte("{"))
		case "/jwks-invalid":
			_, _ = w.Write([]byte("{"))
		case "/jwks-error":
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		case "/jwks-ok":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{"kty": "RSA"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer badDiscovery.Close()
	f.manager.client = badDiscovery.Client()

	if _, err := f.manager.resolveProvider(ctx, OIDCProvider{Issuer: badDiscovery.URL, DiscoveryURL: badDiscovery.URL + "/status"}); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("discovery status err=%v", err)
	}
	if _, err := f.manager.resolveProvider(ctx, OIDCProvider{Issuer: badDiscovery.URL, DiscoveryURL: badDiscovery.URL + "/invalid-json"}); err == nil || !strings.Contains(err.Error(), "decode OIDC discovery") {
		t.Fatalf("discovery decode err=%v", err)
	}
	if err := f.manager.probeJWKS(ctx, badDiscovery.URL+"/jwks-error"); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("JWKS status err=%v", err)
	}
	if err := f.manager.probeJWKS(ctx, badDiscovery.URL+"/jwks-invalid"); err == nil || !strings.Contains(err.Error(), "decode OIDC JWKS") {
		t.Fatalf("JWKS decode err=%v", err)
	}

	secret := "secret"
	manual := OIDCProviderInput{
		Name: "Manual", Enabled: true, Issuer: badDiscovery.URL, ClientID: "client", ClientSecret: &secret,
		AuthorizationEndpoint: badDiscovery.URL + "/authorize", TokenEndpoint: badDiscovery.URL + "/token", JWKSURL: badDiscovery.URL + "/jwks-ok",
	}
	provider, err := f.manager.CreateProvider(ctx, manual)
	if err != nil { t.Fatal(err) }
	if err := f.secrets.DeleteSecret(ctx, oidcSecretName(provider.ID)); err != nil { t.Fatal(err) }
	if _, err := f.manager.Start(ctx, provider.ID, false, "", "", "https://manager.example.test"); err == nil || !strings.Contains(err.Error(), "client secret is unavailable") {
		t.Fatalf("missing Start secret err=%v", err)
	}
	if tested, err := f.manager.TestProvider(ctx, provider.ID); err == nil || tested.LastTestSucceeded || !strings.Contains(err.Error(), "client secret is not configured") {
		t.Fatalf("missing TestProvider secret provider=%+v err=%v", tested, err)
	}

	disabledInput := manual
	disabledInput.Name = "Disabled"
	disabledInput.Enabled = false
	disabledInput.ClientSecret = &secret
	disabled, err := f.manager.CreateProvider(ctx, disabledInput)
	if err != nil { t.Fatal(err) }
	if _, err := f.manager.Start(ctx, disabled.ID, false, "", "", "https://manager.example.test"); err == nil || !strings.Contains(err.Error(), "provider is disabled") {
		t.Fatalf("disabled Start err=%v", err)
	}
	if _, err := f.manager.Start(ctx, "missing", false, "", "", "https://manager.example.test"); err == nil {
		t.Fatal("missing provider Start should fail")
	}
}

func TestOIDCCallbackAndExchangeEarlyErrorBranches(t *testing.T) {
	f := newOIDCFixture(t)
	ctx := t.Context()
	secret := "secret"
	input := OIDCProviderInput{
		Name: "Manual", Enabled: true, Issuer: "https://issuer.example", ClientID: "client", ClientSecret: &secret,
		AuthorizationEndpoint: "https://issuer.example/authorize", TokenEndpoint: "https://issuer.example/token", JWKSURL: "https://issuer.example/jwks",
	}
	provider, err := f.manager.CreateProvider(ctx, input)
	if err != nil { t.Fatal(err) }

	putTransaction := func(state, providerID string, expiresAt time.Time) {
		f.manager.mu.Lock()
		f.manager.transactions[state] = oidcTransaction{ProviderID: providerID, Nonce: "nonce", Verifier: "verifier", ExpiresAt: expiresAt}
		f.manager.mu.Unlock()
	}
	putTransaction("expired", provider.ID, time.Now().Add(-time.Second))
	if _, err := f.manager.CompleteCallback(ctx, provider.ID, "expired", "code", "https://manager.example.test"); err == nil || !strings.Contains(err.Error(), "invalid or expired") {
		t.Fatalf("expired callback err=%v", err)
	}
	putTransaction("wrong-provider", provider.ID, time.Now().Add(time.Minute))
	if _, err := f.manager.CompleteCallback(ctx, "other", "wrong-provider", "code", "https://manager.example.test"); err == nil || !strings.Contains(err.Error(), "invalid or expired") {
		t.Fatalf("wrong provider callback err=%v", err)
	}
	putTransaction("blank-code", provider.ID, time.Now().Add(time.Minute))
	if _, err := f.manager.CompleteCallback(ctx, provider.ID, "blank-code", "   ", "https://manager.example.test"); err == nil || !strings.Contains(err.Error(), "invalid or expired") {
		t.Fatalf("blank code callback err=%v", err)
	}
	putTransaction("missing-provider", "missing", time.Now().Add(time.Minute))
	if _, err := f.manager.CompleteCallback(ctx, "missing", "missing-provider", "code", "https://manager.example.test"); err == nil || !strings.Contains(err.Error(), "provider is unavailable") {
		t.Fatalf("missing provider callback err=%v", err)
	}

	if err := f.secrets.DeleteSecret(ctx, oidcSecretName(provider.ID)); err != nil { t.Fatal(err) }
	putTransaction("missing-secret", provider.ID, time.Now().Add(time.Minute))
	if _, err := f.manager.CompleteCallback(ctx, provider.ID, "missing-secret", "code", "https://manager.example.test"); err == nil || !strings.Contains(err.Error(), "client secret is unavailable") {
		t.Fatalf("missing callback secret err=%v", err)
	}
	if err := f.secrets.SetSecret(ctx, oidcSecretName(provider.ID), secret); err != nil { t.Fatal(err) }
	putTransaction("bad-external", provider.ID, time.Now().Add(time.Minute))
	if _, err := f.manager.CompleteCallback(ctx, provider.ID, "bad-external", "code", "not-a-url"); err == nil || !strings.Contains(err.Error(), "external/public URL") {
		t.Fatalf("bad callback external URL err=%v", err)
	}

	f.manager.mu.Lock()
	f.manager.exchanges["expired-exchange"] = oidcExchange{ExpiresAt: time.Now().Add(-time.Second)}
	f.manager.mu.Unlock()
	if _, err := f.manager.Exchange(" expired-exchange "); err == nil || !strings.Contains(err.Error(), "invalid or expired") {
		t.Fatalf("expired exchange err=%v", err)
	}
	if got := resultSessionID("not-a-token", f.auth); got != "" {
		t.Fatalf("invalid resultSessionID=%q", got)
	}
}
