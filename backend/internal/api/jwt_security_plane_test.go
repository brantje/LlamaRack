package api

import (
	"net/http"
	"testing"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestGeneralSettingsSecurityFieldsRequireJWT(t *testing.T) {
	fixture := newAuthSecurityFixture(t)
	adminUser, err := fixture.auth.Bootstrap(t.Context(), "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	login, err := fixture.auth.LoginBearerWithMetadata(t.Context(), "admin", "correct-horse-battery", "192.0.2.10", "settings-security-test")
	if err != nil {
		t.Fatal(err)
	}
	_, managementSecret, err := fixture.auth.CreateAPIKey(t.Context(), auth.CreateAPIKeyInput{Name: "mgmt", KeyType: auth.APIKeyTypeManagement, OwnerUserID: &adminUser.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, fullSecret, err := fixture.auth.CreateAPIKey(t.Context(), auth.CreateAPIKeyInput{Name: "full", KeyType: auth.APIKeyTypeFull, OwnerUserID: &adminUser.ID})
	if err != nil {
		t.Fatal(err)
	}

	adminHandler := NewAdminHandler(fixture.auth, fixture.settings, nil, fixture.network, func() (llamacpp.Profile, error) {
		return llamacpp.Profile{}, nil
	})
	mux := http.NewServeMux()
	mux.Handle("/api/v1/settings/general", adminHandler)
	handler := ManagementSecurity(fixture.auth, fixture.network, mux)

	beforeOrigins, err := fixture.settings.String(t.Context(), settings.AllowedOrigins)
	if err != nil {
		t.Fatal(err)
	}
	beforeIdle, err := fixture.settings.Int(t.Context(), settings.IdleUnloadSeconds)
	if err != nil {
		t.Fatal(err)
	}

	for _, principal := range []struct {
		name   string
		secret string
	}{{"management", managementSecret}, {"full", fullSecret}} {
		headers := map[string]string{"Authorization": "Bearer " + principal.secret}
		w := adminRequest(t, handler, http.MethodGet, "/api/v1/settings/general", nil, nil, headers)
		if w.Code != http.StatusOK {
			t.Fatalf("%s GET general=%d body=%s", principal.name, w.Code, w.Body.String())
		}

		w = adminRequest(t, handler, http.MethodPut, "/api/v1/settings/general", map[string]any{"allowed_origins": "https://attacker.example"}, nil, headers)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s security setting=%d body=%s", principal.name, w.Code, w.Body.String())
		}
		origins, err := fixture.settings.String(t.Context(), settings.AllowedOrigins)
		if err != nil {
			t.Fatal(err)
		}
		if origins != beforeOrigins {
			t.Fatalf("%s changed allowed origins after denial: %q", principal.name, origins)
		}

		w = adminRequest(t, handler, http.MethodPut, "/api/v1/settings/general", map[string]any{
			"idle_unload_seconds": 123,
			"trusted_proxies":     "10.0.0.0/8",
		}, nil, headers)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s mixed security settings=%d body=%s", principal.name, w.Code, w.Body.String())
		}
		idle, err := fixture.settings.Int(t.Context(), settings.IdleUnloadSeconds)
		if err != nil {
			t.Fatal(err)
		}
		if idle != beforeIdle {
			t.Fatalf("%s partially wrote operational setting: got=%d want=%d", principal.name, idle, beforeIdle)
		}

		w = adminRequest(t, handler, http.MethodPut, "/api/v1/settings/general", map[string]any{"startup_timeout_seconds": 75}, nil, headers)
		if w.Code != http.StatusOK {
			t.Fatalf("%s operational setting=%d body=%s", principal.name, w.Code, w.Body.String())
		}
		startup, err := fixture.settings.Int(t.Context(), settings.StartupTimeoutSeconds)
		if err != nil {
			t.Fatal(err)
		}
		if startup != 75 {
			t.Fatalf("%s startup timeout=%d", principal.name, startup)
		}
	}

	jwtHeaders := map[string]string{"Authorization": "Bearer " + login.AccessToken}
	w := adminRequest(t, handler, http.MethodPut, "/api/v1/settings/general", map[string]any{
		"allowed_origins":     "https://manager.example.test",
		"trusted_proxies":     "192.0.2.0/24",
		"idle_unload_seconds": 321,
	}, nil, jwtHeaders)
	if w.Code != http.StatusOK {
		t.Fatalf("JWT mixed settings=%d body=%s", w.Code, w.Body.String())
	}
	origins, err := fixture.settings.String(t.Context(), settings.AllowedOrigins)
	if err != nil {
		t.Fatal(err)
	}
	if origins != "https://manager.example.test" {
		t.Fatalf("JWT allowed origins=%q", origins)
	}
	idle, err := fixture.settings.Int(t.Context(), settings.IdleUnloadSeconds)
	if err != nil {
		t.Fatal(err)
	}
	if idle != 321 {
		t.Fatalf("JWT idle timeout=%d", idle)
	}
}

func TestAPIKeysCannotBootstrapManagementJWTThroughOIDC(t *testing.T) {
	fixture := newAPIOIDCFixture(t)
	users, err := fixture.auth.ListUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("users=%d", len(users))
	}
	ownerID := users[0].ID
	_, managementSecret, err := fixture.auth.CreateAPIKey(t.Context(), auth.CreateAPIKeyInput{Name: "mgmt", KeyType: auth.APIKeyTypeManagement, OwnerUserID: &ownerID})
	if err != nil {
		t.Fatal(err)
	}
	_, fullSecret, err := fixture.auth.CreateAPIKey(t.Context(), auth.CreateAPIKeyInput{Name: "full", KeyType: auth.APIKeyTypeFull, OwnerUserID: &ownerID})
	if err != nil {
		t.Fatal(err)
	}

	w := adminRequest(t, fixture.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"oidc_jit_provisioning_enabled": false}, nil, fixture.headers())
	if w.Code != http.StatusOK {
		t.Fatalf("disable JIT with JWT=%d body=%s", w.Code, w.Body.String())
	}

	for _, principal := range []struct {
		name   string
		secret string
	}{{"management", managementSecret}, {"full", fullSecret}} {
		headers := map[string]string{"Authorization": "Bearer " + principal.secret}
		w = adminRequest(t, fixture.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"oidc_jit_provisioning_enabled": true}, nil, headers)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s enabling JIT=%d body=%s", principal.name, w.Code, w.Body.String())
		}
		w = adminRequest(t, fixture.secured, http.MethodPost, "/api/v1/admin/auth/providers", fixture.providerInput("Attacker"), nil, headers)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s creating OIDC provider=%d body=%s", principal.name, w.Code, w.Body.String())
		}
		w = adminRequest(t, fixture.secured, http.MethodGet, "/api/v1/admin/auth/providers", nil, nil, headers)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s reading OIDC admin=%d body=%s", principal.name, w.Code, w.Body.String())
		}
	}

	jitEnabled, err := fixture.settings.Bool(t.Context(), settings.OIDCJITProvisioningEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if jitEnabled {
		t.Fatal("API-key attempt changed JIT setting")
	}
	w = adminRequest(t, fixture.secured, http.MethodGet, "/api/v1/admin/auth/providers", nil, nil, fixture.headers())
	if w.Code != http.StatusOK || w.Body.String() != "[]\n" {
		t.Fatalf("provider state changed after denied API-key create: %d %s", w.Code, w.Body.String())
	}
	w = adminRequest(t, fixture.secured, http.MethodGet, "/api/v1/auth/oidc/attacker/start", nil, nil, nil)
	if w.Code >= http.StatusMultipleChoices && w.Code < http.StatusBadRequest {
		t.Fatalf("denied provider unexpectedly started OIDC flow: %d location=%s", w.Code, w.Header().Get("Location"))
	}

	w = adminRequest(t, fixture.secured, http.MethodPost, "/api/v1/admin/auth/providers", fixture.providerInput("Legitimate"), nil, fixture.headers())
	if w.Code != http.StatusCreated {
		t.Fatalf("JWT provider create=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, fixture.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"oidc_jit_provisioning_enabled": true}, nil, fixture.headers())
	if w.Code != http.StatusOK {
		t.Fatalf("JWT enabling JIT=%d body=%s", w.Code, w.Body.String())
	}
}
