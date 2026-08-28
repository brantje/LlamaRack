package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
	"github.com/golang-jwt/jwt/v5"
)

type memoryOIDCSecrets struct {
	mu   sync.Mutex
	data map[string]string
}

func newMemoryOIDCSecrets() *memoryOIDCSecrets { return &memoryOIDCSecrets{data: map[string]string{}} }
func (s *memoryOIDCSecrets) GetSecret(_ context.Context, name string) (string, error) {
	s.mu.Lock(); defer s.mu.Unlock(); return s.data[name], nil
}
func (s *memoryOIDCSecrets) SetSecret(_ context.Context, name, value string) error {
	s.mu.Lock(); defer s.mu.Unlock(); s.data[name] = value; return nil
}
func (s *memoryOIDCSecrets) DeleteSecret(_ context.Context, name string) error {
	s.mu.Lock(); defer s.mu.Unlock(); delete(s.data, name); return nil
}
func (s *memoryOIDCSecrets) SecretConfigured(_ context.Context, name string) (bool, error) {
	s.mu.Lock(); defer s.mu.Unlock(); _, ok := s.data[name]; return ok, nil
}

type oidcFixture struct {
	auth     *Service
	settings *settings.Service
	secrets  *memoryOIDCSecrets
	manager  *OIDCManager
}

func newOIDCFixture(t *testing.T) *oidcFixture {
	t.Helper()
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "oidc.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = db.Close() })
	managerSettings := settings.New(db, settings.Defaults{SessionLifetime: time.Hour, StartupTimeout: time.Minute, AlwaysOnReconcile: time.Second})
	authService := New(db, time.Hour)
	secrets := newMemoryOIDCSecrets()
	return &oidcFixture{auth: authService, settings: managerSettings, secrets: secrets, manager: NewOIDCManager(authService, managerSettings, secrets)}
}

type testOIDCProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	mu     sync.Mutex
	nonce  string
}

func newTestOIDCProvider(t *testing.T) *testOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil { t.Fatal(err) }
	provider := &testOIDCProvider{key: key}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(w, map[string]any{
				"issuer": server.URL,
				"authorization_endpoint": server.URL + "/authorize",
				"token_endpoint": server.URL + "/token",
				"jwks_uri": server.URL + "/jwks",
			})
		case "/jwks":
			n := base64.RawURLEncoding.EncodeToString(provider.key.PublicKey.N.Bytes())
			e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(provider.key.PublicKey.E)).Bytes())
			writeTestJSON(w, map[string]any{"keys": []map[string]any{{"kty": "RSA", "kid": "test-key", "use": "sig", "alg": "RS256", "n": n, "e": e}}})
		case "/token":
			provider.mu.Lock(); nonce := provider.nonce; provider.mu.Unlock()
			claims := jwt.MapClaims{
				"iss": server.URL, "sub": "subject-1", "aud": "manager-client",
				"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Add(-time.Second).Unix(),
				"nonce": nonce, "preferred_username": "oidc-user", "email": "oidc@example.test",
			}
			encoded := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			encoded.Header["kid"] = "test-key"
			signed, err := encoded.SignedString(provider.key)
			if err != nil { http.Error(w, err.Error(), http.StatusInternalServerError); return }
			writeTestJSON(w, map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 3600, "id_token": signed})
		default:
			http.NotFound(w, r)
		}
	}))
	provider.server = server
	t.Cleanup(server.Close)
	return provider
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (p *testOIDCProvider) input(secret *string) OIDCProviderInput {
	return OIDCProviderInput{Name: "Primary", Enabled: true, Issuer: p.server.URL, ClientID: "manager-client", ClientSecret: secret, Scopes: []string{"profile email", "email"}}
}

func TestOIDCProviderLifecycleTestingAndLockoutPolicy(t *testing.T) {
	f := newOIDCFixture(t)
	ctx := t.Context()
	idp := newTestOIDCProvider(t)
	f.manager.client = idp.server.Client()

	if _, err := f.manager.CreateProvider(ctx, OIDCProviderInput{}); err == nil { t.Fatal("missing provider fields should fail") }
	badSecret := ""
	if _, err := f.manager.CreateProvider(ctx, OIDCProviderInput{Name: "x", Issuer: "not-a-url", ClientID: "id", ClientSecret: &badSecret}); err == nil { t.Fatal("invalid issuer should fail") }
	validNoSecret := idp.input(nil)
	if _, err := f.manager.CreateProvider(ctx, validNoSecret); err == nil { t.Fatal("missing client secret should fail") }

	secret := "client-secret"
	provider, err := f.manager.CreateProvider(ctx, idp.input(&secret))
	if err != nil { t.Fatal(err) }
	if provider.ID == "" || !provider.Enabled || !provider.SecretConfigured || provider.UsernameClaim != "preferred_username" {
		t.Fatalf("unexpected provider: %+v", provider)
	}
	if got := strings.Join(provider.Scopes, ","); got != "openid,profile,email" { t.Fatalf("normalized scopes=%q", got) }

	listed, err := f.manager.ListProviders(ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != provider.ID { t.Fatalf("providers=%+v err=%v", listed, err) }
	public, err := f.manager.PublicProviders(ctx)
	if err != nil || len(public) != 1 || public[0].Name != "Primary" { t.Fatalf("public=%+v err=%v", public, err) }

	tested, err := f.manager.TestProvider(ctx, provider.ID)
	if err != nil || !tested.LastTestSucceeded || tested.LastTestedAt == nil { t.Fatalf("tested=%+v err=%v", tested, err) }
	if usable, err := f.manager.CanDisableLocalLogin(ctx); err != nil || !usable { t.Fatalf("usable=%v err=%v", usable, err) }
	if _, err := f.settings.Set(ctx, settings.LocalLoginEnabled, false); err != nil { t.Fatal(err) }

	unchanged := idp.input(nil)
	kept, err := f.manager.UpdateProvider(ctx, provider.ID, unchanged)
	if err != nil || !kept.LastTestSucceeded { t.Fatalf("unchanged update=%+v err=%v", kept, err) }
	changed := unchanged
	changed.Name = "Changed"
	if _, err := f.manager.UpdateProvider(ctx, provider.ID, changed); !errors.Is(err, ErrAuthLockoutRisk) { t.Fatalf("changed last provider err=%v", err) }
	disabled := unchanged
	disabled.Enabled = false
	if _, err := f.manager.UpdateProvider(ctx, provider.ID, disabled); !errors.Is(err, ErrAuthLockoutRisk) { t.Fatalf("disable last provider err=%v", err) }
	if err := f.manager.DeleteProvider(ctx, provider.ID); !errors.Is(err, ErrAuthLockoutRisk) { t.Fatalf("delete last provider err=%v", err) }

	secondSecret := "second-secret"
	secondInput := idp.input(&secondSecret)
	secondInput.Name = "Secondary"
	second, err := f.manager.CreateProvider(ctx, secondInput)
	if err != nil { t.Fatal(err) }
	if _, err := f.manager.TestProvider(ctx, second.ID); err != nil { t.Fatal(err) }
	if err := f.manager.DeleteProvider(ctx, provider.ID); err != nil { t.Fatal(err) }
	if _, err := f.manager.GetProvider(ctx, provider.ID); err == nil { t.Fatal("deleted provider should not be readable") }

	if _, err := f.settings.Set(ctx, settings.LocalLoginEnabled, true); err != nil { t.Fatal(err) }
	disabledSecond := secondInput
	disabledSecond.ClientSecret = nil
	disabledSecond.Enabled = false
	updated, err := f.manager.UpdateProvider(ctx, second.ID, disabledSecond)
	if err != nil || updated.Enabled || updated.LastTestSucceeded || updated.LastTestedAt != nil { t.Fatalf("disabled provider=%+v err=%v", updated, err) }
	public, err = f.manager.PublicProviders(ctx)
	if err != nil || len(public) != 0 { t.Fatalf("disabled public providers=%+v err=%v", public, err) }
	if err := f.manager.DeleteProvider(ctx, second.ID); err != nil { t.Fatal(err) }
}

func TestOIDCStartCallbackAndOneTimeExchange(t *testing.T) {
	f := newOIDCFixture(t)
	ctx := t.Context()
	idp := newTestOIDCProvider(t)
	f.manager.client = idp.server.Client()
	secret := "client-secret"
	provider, err := f.manager.CreateProvider(ctx, idp.input(&secret))
	if err != nil { t.Fatal(err) }

	if _, err := f.manager.Start(ctx, provider.ID, false, "192.0.2.1", "agent", ""); err == nil { t.Fatal("OIDC start without external URL should fail") }
	authURL, err := f.manager.Start(ctx, provider.ID, true, "192.0.2.1", strings.Repeat("a", 600), "https://manager.example.test/base")
	if err != nil { t.Fatal(err) }
	parsed, err := url.Parse(authURL)
	if err != nil { t.Fatal(err) }
	state := parsed.Query().Get("state")
	if state == "" || parsed.Query().Get("nonce") == "" || parsed.Query().Get("code_challenge") == "" || parsed.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL missing OIDC/PKCE fields: %s", authURL)
	}
	f.manager.mu.Lock()
	transaction := f.manager.transactions[state]
	f.manager.mu.Unlock()
	if transaction.ProviderID != provider.ID || !transaction.Remember || transaction.RemoteAddr != "192.0.2.1" || len(transaction.UserAgent) != 512 {
		t.Fatalf("transaction=%+v", transaction)
	}
	idp.mu.Lock(); idp.nonce = transaction.Nonce; idp.mu.Unlock()

	if _, err := f.manager.CompleteCallback(ctx, provider.ID, "missing", "code", "https://manager.example.test/base"); err == nil { t.Fatal("missing state should fail") }
	frontendURL, err := f.manager.CompleteCallback(ctx, provider.ID, state, "code", "https://manager.example.test/base")
	if err != nil { t.Fatal(err) }
	redirect, err := url.Parse(frontendURL)
	if err != nil { t.Fatal(err) }
	code := redirect.Query().Get("oidc_exchange")
	if code == "" { t.Fatalf("frontend exchange URL missing code: %s", frontendURL) }
	result, err := f.manager.Exchange(code)
	if err != nil { t.Fatal(err) }
	if !result.Remember || result.AccessToken == "" || result.TokenType != "Bearer" || result.User.Username != "oidc-user" {
		t.Fatalf("exchange result=%+v", result)
	}
	if _, _, err := f.auth.AuthenticateBearer(ctx, result.AccessToken); err != nil { t.Fatalf("OIDC bearer token invalid: %v", err) }
	if _, err := f.manager.Exchange(code); err == nil { t.Fatal("exchange code should be one-time") }

	if _, err := f.manager.CompleteCallback(ctx, provider.ID, state, "code", "https://manager.example.test/base"); err == nil { t.Fatal("OIDC state should be one-time") }
	f.manager.mu.Lock()
	f.manager.transactions["expired"] = oidcTransaction{ExpiresAt: time.Now().Add(-time.Second)}
	f.manager.exchanges["expired"] = oidcExchange{ExpiresAt: time.Now().Add(-time.Second)}
	f.manager.cleanupLocked(time.Now())
	_, transactionStillPresent := f.manager.transactions["expired"]
	_, exchangeStillPresent := f.manager.exchanges["expired"]
	f.manager.mu.Unlock()
	if transactionStillPresent || exchangeStillPresent { t.Fatal("expired OIDC transient state was not cleaned up") }
}

func TestOIDCIdentityResolutionLinkingAndHelpers(t *testing.T) {
	f := newOIDCFixture(t)
	ctx := t.Context()
	idp := newTestOIDCProvider(t)
	secret := "client-secret"
	provider, err := f.manager.CreateProvider(ctx, idp.input(&secret))
	if err != nil { t.Fatal(err) }
	admin, err := f.auth.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil { t.Fatal(err) }
	operator, err := f.auth.CreateUser(ctx, "operator", "another-correct-password")
	if err != nil { t.Fatal(err) }

	if _, err := f.manager.LinkIdentity(ctx, operator.ID, provider.ID, "https://wrong.example", "subject"); err == nil { t.Fatal("mismatched issuer should fail") }
	if _, err := f.manager.LinkIdentity(ctx, operator.ID, provider.ID, "", " "); err == nil { t.Fatal("blank subject should fail") }
	identity, err := f.manager.LinkIdentity(ctx, operator.ID, provider.ID, "", "operator-subject")
	if err != nil { t.Fatal(err) }
	if identity.Issuer != provider.Issuer || identity.UserID != operator.ID { t.Fatalf("identity=%+v", identity) }
	items, err := f.manager.ListIdentities(ctx, 0)
	if err != nil || len(items) != 1 { t.Fatalf("identities=%+v err=%v", items, err) }
	items, err = f.manager.ListIdentities(ctx, operator.ID)
	if err != nil || len(items) != 1 || items[0].ID != identity.ID { t.Fatalf("user identities=%+v err=%v", items, err) }
	resolved, err := f.manager.resolveIdentity(ctx, provider, "operator-subject", "ignored")
	if err != nil || resolved.ID != operator.ID { t.Fatalf("existing identity resolved=%+v err=%v", resolved, err) }
	if err := f.manager.UnlinkIdentity(ctx, identity.ID); err != nil { t.Fatal(err) }
	if err := f.manager.UnlinkIdentity(ctx, identity.ID); !errors.Is(err, context.Canceled) && err == nil { t.Fatal("unlinking missing identity should fail") }

	if _, err := f.settings.Set(ctx, settings.OIDCJITProvisioningEnabled, false); err != nil { t.Fatal(err) }
	if _, err := f.manager.resolveIdentity(ctx, provider, "jit-off", "new-user"); !errors.Is(err, ErrOIDCLinkRequired) { t.Fatalf("JIT disabled err=%v", err) }
	if _, err := f.settings.Set(ctx, settings.OIDCJITProvisioningEnabled, true); err != nil { t.Fatal(err) }
	created, err := f.manager.resolveIdentity(ctx, provider, "jit-new", "new-user")
	if err != nil || created.Username != "new-user" { t.Fatalf("JIT user=%+v err=%v", created, err) }
	if _, err := f.manager.resolveIdentity(ctx, provider, "too-short", "x"); err == nil { t.Fatal("too-short JIT username should fail") }

	if _, err := f.manager.resolveIdentity(ctx, provider, "admin-subject", admin.Username); !errors.Is(err, ErrOIDCUsernameTaken) { t.Fatalf("username collision err=%v", err) }
	if _, err := f.settings.Set(ctx, settings.OIDCAutoLinkEnabled, true); err != nil { t.Fatal(err) }
	autoLinked, err := f.manager.resolveIdentity(ctx, provider, "admin-subject", admin.Username)
	if err != nil || autoLinked.ID != admin.ID { t.Fatalf("auto-linked user=%+v err=%v", autoLinked, err) }

	if got := normalizeScopes([]string{"email, profile", "email", "openid"}); strings.Join(got, ",") != "openid,email,profile" { t.Fatalf("normalizeScopes=%v", got) }
	if !stringSlicesEqual([]string{"email", "openid"}, []string{"openid", "email"}) || stringSlicesEqual([]string{"openid"}, []string{"openid", "email"}) { t.Fatal("stringSlicesEqual mismatch") }
	if got := oidcUsername(provider, map[string]any{"email": " person@example.test "}, "sub"); got != "person@example.test" { t.Fatalf("oidcUsername email=%q", got) }
	if got := oidcUsername(provider, map[string]any{}, "sub"); got != "oidc-sub" { t.Fatalf("oidcUsername fallback=%q", got) }
	if _, err := oidcCallbackURL("", provider.ID); err == nil { t.Fatal("empty callback base should fail") }
	callback, err := oidcCallbackURL("https://manager.example.test/base/?old=1#fragment", provider.ID)
	if err != nil || !strings.Contains(callback, "/base/api/v1/auth/oidc/") || strings.Contains(callback, "old=1") || strings.Contains(callback, "fragment") { t.Fatalf("callback=%q err=%v", callback, err) }
	if _, err := frontendExchangeURL("not-a-url", "code"); err == nil { t.Fatal("invalid frontend base should fail") }
	frontend, err := frontendExchangeURL("https://manager.example.test/base?keep=1#fragment", "code")
	if err != nil || !strings.Contains(frontend, "oidc_exchange=code") || !strings.Contains(frontend, "keep=1") || strings.Contains(frontend, "fragment") { t.Fatalf("frontend=%q err=%v", frontend, err) }
}

func TestOIDCDiscoveryAndProviderErrorPaths(t *testing.T) {
	f := newOIDCFixture(t)
	ctx := t.Context()
	badDiscovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(w, map[string]any{"issuer": "https://other.example", "authorization_endpoint": "https://a.example", "token_endpoint": "https://t.example", "jwks_uri": "https://j.example"})
		case "/jwks":
			writeTestJSON(w, map[string]any{"keys": []any{}})
		default:
			http.Error(w, "bad", http.StatusBadGateway)
		}
	}))
	defer badDiscovery.Close()
	f.manager.client = badDiscovery.Client()
	secret := "secret"
	provider, err := f.manager.CreateProvider(ctx, OIDCProviderInput{Name: "Bad discovery", Enabled: true, Issuer: badDiscovery.URL, ClientID: "client", ClientSecret: &secret})
	if err != nil { t.Fatal(err) }
	if _, err := f.manager.TestProvider(ctx, provider.ID); err == nil { t.Fatal("issuer mismatch should fail provider test") }
	failed, err := f.manager.GetProvider(ctx, provider.ID)
	if err != nil || failed.LastTestSucceeded || failed.LastTestedAt == nil { t.Fatalf("failed provider test state=%+v err=%v", failed, err) }

	if _, err := f.manager.resolveProvider(ctx, OIDCProvider{Issuer: "https://issuer.example", AuthorizationEndpoint: "file:///authorize", TokenEndpoint: "https://token.example", JWKSURL: "https://jwks.example"}); err == nil {
		t.Fatal("invalid explicit endpoint should fail")
	}
	if err := f.manager.probeJWKS(ctx, badDiscovery.URL+"/jwks"); err == nil { t.Fatal("empty JWKS should fail") }
	if _, err := f.manager.GetProvider(ctx, "missing"); err == nil { t.Fatal("missing provider should fail") }
	if err := f.manager.DeleteProvider(ctx, "missing"); err == nil { t.Fatal("deleting missing provider should fail") }
}
