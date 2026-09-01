package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
)

func apiKeyHandler(t *testing.T, f *apiFixture) http.Handler {
	t.Helper()
	cookie := bootstrapAndLogin(t, f)
	user, session, err := f.auth.SessionUserWithSession(t.Context(), cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAPIKeysHandler(f.auth)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), managementAuthContextKey{}, managementAuthContext{User: user, Session: session})
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestAPIKeyLifecycleRetainsRevocationHistory(t *testing.T) {
	f := newAPIFixture(t, nil)
	handler := apiKeyHandler(t, f)

	created := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys", map[string]string{"name": "toggle-key"}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var result struct {
		Key    auth.APIKey `json:"key"`
		Secret string      `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Key.ID == "" || result.Secret == "" {
		t.Fatalf("invalid create response: %+v", result)
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), result.Secret); err != nil {
		t.Fatalf("new key should authenticate: %v", err)
	}

	disabled := doRequest(t, handler, http.MethodPatch, "/api/v1/api-keys/"+result.Key.ID, map[string]bool{"enabled": false}, nil)
	if disabled.Code != http.StatusNoContent {
		t.Fatalf("disable status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), result.Secret); err == nil {
		t.Fatal("disabled key should not authenticate")
	}

	enabled := doRequest(t, handler, http.MethodPatch, "/api/v1/api-keys/"+result.Key.ID, map[string]bool{"enabled": true}, nil)
	if enabled.Code != http.StatusNoContent {
		t.Fatalf("enable status=%d body=%s", enabled.Code, enabled.Body.String())
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), result.Secret); err != nil {
		t.Fatalf("re-enabled key should authenticate: %v", err)
	}

	revoked := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys/"+result.Key.ID+"/revoke", nil, nil)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), result.Secret); err == nil {
		t.Fatal("revoked key should not authenticate")
	}
	listed := doRequest(t, handler, http.MethodGet, "/api/v1/api-keys", nil, nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), result.Key.ID) || !strings.Contains(listed.Body.String(), `"enabled":false`) || !strings.Contains(listed.Body.String(), `"revoked_at":`) {
		t.Fatalf("revoked key metadata should remain listed: status=%d body=%s", listed.Code, listed.Body.String())
	}
}

func TestAPIKeyRotationRevokesOldSecretAndReturnsNewSecretOnce(t *testing.T) {
	f := newAPIFixture(t, nil)
	handler := apiKeyHandler(t, f)

	created := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys", map[string]string{"name": "rotate-key"}, nil)
	var original struct {
		Key    auth.APIKey `json:"key"`
		Secret string      `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}

	rotated := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys/"+original.Key.ID+"/rotate", nil, nil)
	if rotated.Code != http.StatusCreated {
		t.Fatalf("rotate status=%d body=%s", rotated.Code, rotated.Body.String())
	}
	var replacement struct {
		Key    auth.APIKey `json:"key"`
		Secret string      `json:"secret"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &replacement); err != nil {
		t.Fatal(err)
	}
	if replacement.Key.ID == "" || replacement.Key.ID == original.Key.ID || replacement.Secret == "" || replacement.Secret == original.Secret {
		t.Fatalf("invalid replacement: %+v", replacement)
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), original.Secret); err == nil {
		t.Fatal("rotated original secret should fail immediately")
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), replacement.Secret); err != nil {
		t.Fatalf("replacement secret should authenticate: %v", err)
	}
	listed := doRequest(t, handler, http.MethodGet, "/api/v1/api-keys", nil, nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), original.Key.ID) || !strings.Contains(listed.Body.String(), replacement.Key.ID) || strings.Contains(listed.Body.String(), replacement.Secret) {
		t.Fatalf("rotation history/plaintext contract violated: %s", listed.Body.String())
	}
}

func TestAPIKeyMutationValidation(t *testing.T) {
	f := newAPIFixture(t, nil)
	handler := apiKeyHandler(t, f)

	unauthorized := doRequest(t, NewAPIKeysHandler(f.auth), http.MethodGet, "/api/v1/api-keys", nil, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized list=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	missingEnabled := doRequest(t, handler, http.MethodPatch, "/api/v1/api-keys/missing", map[string]string{}, nil)
	if missingEnabled.Code != http.StatusBadRequest || !strings.Contains(missingEnabled.Body.String(), "enabled is required") {
		t.Fatalf("missing enabled status=%d body=%s", missingEnabled.Code, missingEnabled.Body.String())
	}
	missingKey := doRequest(t, handler, http.MethodPatch, "/api/v1/api-keys/missing", map[string]bool{"enabled": false}, nil)
	if missingKey.Code != http.StatusNotFound {
		t.Fatalf("missing key status=%d body=%s", missingKey.Code, missingKey.Body.String())
	}
	wrongMethod := doRequest(t, handler, http.MethodGet, "/api/v1/api-keys/missing/revoke", nil, nil)
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong revoke method status=%d body=%s", wrongMethod.Code, wrongMethod.Body.String())
	}
	badPath := doRequest(t, handler, http.MethodGet, "/api/v1/api-keys/a/b/c", nil, nil)
	if badPath.Code != http.StatusNotFound {
		t.Fatalf("bad path status=%d body=%s", badPath.Code, badPath.Body.String())
	}
}
