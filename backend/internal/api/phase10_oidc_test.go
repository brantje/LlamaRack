package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/database"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

type apiOIDCSecrets struct {
	mu   sync.Mutex
	data map[string]string
}

func (s *apiOIDCSecrets) GetSecret(_ context.Context, name string) (string, error) {
	s.mu.Lock(); defer s.mu.Unlock(); return s.data[name], nil
}
func (s *apiOIDCSecrets) SetSecret(_ context.Context, name, value string) error {
	s.mu.Lock(); defer s.mu.Unlock(); s.data[name] = value; return nil
}
func (s *apiOIDCSecrets) DeleteSecret(_ context.Context, name string) error {
	s.mu.Lock(); defer s.mu.Unlock(); delete(s.data, name); return nil
}
func (s *apiOIDCSecrets) SecretConfigured(_ context.Context, name string) (bool, error) {
	s.mu.Lock(); defer s.mu.Unlock(); _, ok := s.data[name]; return ok, nil
}

type apiOIDCFixture struct {
	raw      http.Handler
	secured  http.Handler
	auth     *auth.Service
	oidc     *auth.OIDCManager
	settings *settings.Service
	secrets  *apiOIDCSecrets
	idp      *httptest.Server
	token    string
}

func newAPIOIDCFixture(t *testing.T) *apiOIDCFixture {
	t.Helper()
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "phase10-oidc.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = db.Close() })
	managerSettings := settings.New(db, settings.Defaults{SessionLifetime: time.Hour, AllowedOrigins: "https://manager.example.test", StartupTimeout: time.Minute, AlwaysOnReconcile: time.Second})
	authService := auth.New(db, time.Hour)
	secrets := &apiOIDCSecrets{data: map[string]string{}}
	oidcManager := auth.NewOIDCManager(authService, managerSettings, secrets)
	network := managersecurity.NewNetwork(managerSettings)
	raw := NewPhase10OIDCHandler(oidcManager, authService, managerSettings, network)
	secured := ManagementSecurity(authService, network, raw)
	if _, err := authService.Bootstrap(t.Context(), "admin", "correct-horse-battery"); err != nil { t.Fatal(err) }
	login, err := authService.LoginBearerWithMetadata(t.Context(), "admin", "correct-horse-battery", "127.0.0.1", "oidc-api-test")
	if err != nil { t.Fatal(err) }

	var idp *httptest.Server
	idp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": idp.URL,
				"authorization_endpoint": idp.URL + "/authorize",
				"token_endpoint": idp.URL + "/token",
				"jwks_uri": idp.URL + "/jwks",
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(idp.Close)
	return &apiOIDCFixture{raw: raw, secured: secured, auth: authService, oidc: oidcManager, settings: managerSettings, secrets: secrets, idp: idp, token: login.AccessToken}
}

func (f *apiOIDCFixture) headers() map[string]string { return map[string]string{"Authorization": "Bearer " + f.token} }
func (f *apiOIDCFixture) providerInput(name string) map[string]any {
	return map[string]any{"name": name, "enabled": true, "issuer": f.idp.URL, "client_id": "manager", "client_secret": "client-secret", "scopes": []string{"openid", "profile"}}
}

func decodeAPIJSON(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(recorder.Body.Bytes(), target); err != nil { t.Fatalf("decode %s: %v", recorder.Body.String(), err) }
}

func TestPhase10OIDCAdminProviderSettingsAndIdentityRoutes(t *testing.T) {
	f := newAPIOIDCFixture(t)

	w := phase10Request(t, f.secured, http.MethodGet, "/api/v1/auth/providers", nil, nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"local_login_enabled":true`) { t.Fatalf("public providers status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.secured, http.MethodGet, "/api/v1/admin/auth/settings", nil, nil, nil)
	if w.Code != http.StatusUnauthorized { t.Fatalf("admin settings without bearer=%d", w.Code) }
	w = phase10Request(t, f.secured, http.MethodGet, "/api/v1/admin/auth/settings", nil, nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "oidc_jit_provisioning_enabled") { t.Fatalf("auth settings status=%d body=%s", w.Code, w.Body.String()) }

	w = phase10Request(t, f.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"local_login_enabled": false}, nil, f.headers())
	if w.Code != http.StatusConflict { t.Fatalf("unsafe local disable=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"oidc_jit_provisioning_enabled": false, "oidc_auto_link_enabled": true, "external_url": "https://manager.example.test"}, nil, f.headers())
	if w.Code != http.StatusOK { t.Fatalf("auth settings update=%d body=%s", w.Code, w.Body.String()) }

	w = phase10Request(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers", f.providerInput("Primary"), nil, f.headers())
	if w.Code != http.StatusCreated { t.Fatalf("create provider=%d body=%s", w.Code, w.Body.String()) }
	var provider auth.OIDCProvider
	decodeAPIJSON(t, w, &provider)
	if provider.ID == "" || !provider.SecretConfigured { t.Fatalf("provider=%+v", provider) }
	w = phase10Request(t, f.secured, http.MethodGet, "/api/v1/admin/auth/providers", nil, nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), provider.ID) { t.Fatalf("list providers=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.secured, http.MethodGet, "/api/v1/admin/auth/providers/"+provider.ID, nil, nil, f.headers())
	if w.Code != http.StatusOK { t.Fatalf("get provider=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers/"+provider.ID+"/test", nil, nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"last_test_succeeded":true`) { t.Fatalf("test provider=%d body=%s", w.Code, w.Body.String()) }

	w = phase10Request(t, f.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"local_login_enabled": false, "oidc_jit_provisioning_enabled": true}, nil, f.headers())
	if w.Code != http.StatusOK { t.Fatalf("safe local disable=%d body=%s", w.Code, w.Body.String()) }
	changed := f.providerInput("Changed")
	delete(changed, "client_secret")
	w = phase10Request(t, f.secured, http.MethodPut, "/api/v1/admin/auth/providers/"+provider.ID, changed, nil, f.headers())
	if w.Code != http.StatusConflict { t.Fatalf("unsafe provider change=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.secured, http.MethodDelete, "/api/v1/admin/auth/providers/"+provider.ID, nil, nil, f.headers())
	if w.Code != http.StatusConflict { t.Fatalf("unsafe provider delete=%d body=%s", w.Code, w.Body.String()) }

	w = phase10Request(t, f.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"local_login_enabled": true}, nil, f.headers())
	if w.Code != http.StatusOK { t.Fatalf("re-enable local login=%d body=%s", w.Code, w.Body.String()) }

	w = phase10Request(t, f.secured, http.MethodPost, "/api/v1/admin/auth/identities", map[string]any{"user_id": 1, "provider_id": provider.ID, "issuer": provider.Issuer, "subject": "manual-subject"}, nil, f.headers())
	if w.Code != http.StatusCreated { t.Fatalf("link identity=%d body=%s", w.Code, w.Body.String()) }
	var identity auth.ExternalIdentity
	decodeAPIJSON(t, w, &identity)
	w = phase10Request(t, f.secured, http.MethodGet, "/api/v1/admin/auth/identities", nil, nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), identity.ID) { t.Fatalf("list identities=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.secured, http.MethodGet, "/api/v1/me/identities", nil, nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), identity.ID) { t.Fatalf("my identities=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.secured, http.MethodDelete, "/api/v1/admin/auth/identities/"+identity.ID, nil, nil, f.headers())
	if w.Code != http.StatusNoContent { t.Fatalf("unlink identity=%d body=%s", w.Code, w.Body.String()) }

	w = phase10Request(t, f.secured, http.MethodPost, "/api/v1/auth/ws-ticket", nil, nil, f.headers())
	if w.Code != http.StatusCreated { t.Fatalf("ws ticket=%d body=%s", w.Code, w.Body.String()) }
	var ticket struct { Ticket string `json:"ticket"`; ExpiresAt int64 `json:"expires_at"` }
	decodeAPIJSON(t, w, &ticket)
	if ticket.Ticket == "" || ticket.ExpiresAt <= time.Now().Unix() { t.Fatalf("ticket=%+v", ticket) }
	if _, _, err := f.auth.ConsumeWebSocketTicket(t.Context(), ticket.Ticket); err != nil { t.Fatalf("consume ws ticket: %v", err) }

	w = phase10Request(t, f.secured, http.MethodPost, "/api/v1/auth/oidc/exchange", map[string]string{"code": "missing"}, nil, nil)
	if w.Code != http.StatusUnauthorized { t.Fatalf("invalid exchange=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.secured, http.MethodDelete, "/api/v1/admin/auth/providers/"+provider.ID, nil, nil, f.headers())
	if w.Code != http.StatusNoContent { t.Fatalf("delete provider=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.secured, http.MethodGet, "/api/v1/admin/auth/providers/missing", nil, nil, f.headers())
	if w.Code != http.StatusNotFound { t.Fatalf("missing provider=%d body=%s", w.Code, w.Body.String()) }
}

func TestPhase10OIDCBrowserStateAndRouteEdges(t *testing.T) {
	f := newAPIOIDCFixture(t)
	if _, err := f.settings.Set(t.Context(), settings.ExternalURL, "https://manager.example.test"); err != nil { t.Fatal(err) }
	secret := "client-secret"
	provider, err := f.oidc.CreateProvider(t.Context(), auth.OIDCProviderInput{Name: "Browser", Enabled: true, Issuer: f.idp.URL, ClientID: "manager", ClientSecret: &secret, Scopes: []string{"openid"}})
	if err != nil { t.Fatal(err) }

	w := phase10Request(t, f.secured, http.MethodGet, "/api/v1/auth/oidc/"+provider.ID+"/start?remember=true", nil, nil, nil)
	if w.Code != http.StatusFound { t.Fatalf("OIDC start=%d body=%s", w.Code, w.Body.String()) }
	location := w.Header().Get("Location")
	parsed, err := url.Parse(location)
	if err != nil || parsed.Query().Get("state") == "" { t.Fatalf("OIDC redirect=%q err=%v", location, err) }
	var stateCookie *http.Cookie
	for _, cookie := range w.Result().Cookies() { if cookie.Name == oidcStateCookie { stateCookie = cookie } }
	if stateCookie == nil || !stateCookie.HttpOnly || stateCookie.SameSite != http.SameSiteLaxMode { t.Fatalf("state cookie=%+v", stateCookie) }

	badCallback := "/api/v1/auth/oidc/" + provider.ID + "/callback?state=wrong&code=code"
	w = phase10Request(t, f.secured, http.MethodGet, badCallback, nil, []*http.Cookie{stateCookie}, nil)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "OIDC authentication failed") { t.Fatalf("bad browser state=%d body=%s", w.Code, w.Body.String()) }

	for _, tc := range []struct{ method, path string; status int }{
		{http.MethodPost, "/api/v1/auth/oidc/" + provider.ID + "/start", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v1/auth/oidc/" + provider.ID + "/callback", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/auth/oidc/" + provider.ID + "/unknown", http.StatusNotFound},
		{http.MethodGet, "/api/v1/auth/oidc/too/many/parts", http.StatusNotFound},
		{http.MethodPut, "/api/v1/admin/auth/providers", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/admin/auth/identities/missing", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/not-found", http.StatusNotFound},
	} {
		h := f.raw
		w = phase10Request(t, h, tc.method, tc.path, nil, nil, nil)
		if w.Code != tc.status { t.Fatalf("%s %s=%d want=%d body=%s", tc.method, tc.path, w.Code, tc.status, w.Body.String()) }
	}
}

func TestPhase10OIDCLockoutPolicySerializesConcurrentMutations(t *testing.T) {
	f := newAPIOIDCFixture(t)
	for iteration := 0; iteration < 12; iteration++ {
		if _, err := f.settings.Set(t.Context(), settings.LocalLoginEnabled, true); err != nil { t.Fatal(err) }
		secret := "secret"
		provider, err := f.oidc.CreateProvider(t.Context(), auth.OIDCProviderInput{Name: "Concurrent", Enabled: true, Issuer: f.idp.URL, ClientID: "manager", ClientSecret: &secret, Scopes: []string{"openid"}})
		if err != nil { t.Fatal(err) }
		if _, err := f.oidc.TestProvider(t.Context(), provider.ID); err != nil { t.Fatal(err) }

		start := make(chan struct{})
		statuses := make(chan int, 2)
		go func() {
			<-start
			w := phase10Request(t, f.raw, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"local_login_enabled": false}, nil, nil)
			statuses <- w.Code
		}()
		go func() {
			<-start
			w := phase10Request(t, f.raw, http.MethodDelete, "/api/v1/admin/auth/providers/"+provider.ID, nil, nil, nil)
			statuses <- w.Code
		}()
		close(start)
		first, second := <-statuses, <-statuses
		successes, conflicts := 0, 0
		for _, status := range []int{first, second} {
			switch status {
			case http.StatusOK, http.StatusNoContent: successes++
			case http.StatusConflict: conflicts++
			default: t.Fatalf("iteration %d unexpected statuses %d/%d", iteration, first, second)
			}
		}
		if successes != 1 || conflicts != 1 { t.Fatalf("iteration %d allowed lockout-sensitive mutations %d/%d", iteration, first, second) }
		localEnabled, err := f.settings.Bool(t.Context(), settings.LocalLoginEnabled)
		if err != nil { t.Fatal(err) }
		usableOIDC, err := f.oidc.CanDisableLocalLogin(t.Context())
		if err != nil { t.Fatal(err) }
		if !localEnabled && !usableOIDC { t.Fatalf("iteration %d left no usable login method", iteration) }

		if _, err := f.settings.Set(t.Context(), settings.LocalLoginEnabled, true); err != nil { t.Fatal(err) }
		if _, err := f.oidc.GetProvider(t.Context(), provider.ID); err == nil {
			if err := f.oidc.DeleteProvider(t.Context(), provider.ID); err != nil { t.Fatal(err) }
		}
	}
}
