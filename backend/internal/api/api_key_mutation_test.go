package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
)

func TestAPIKeyEnableDisableAndPermanentRevoke(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)

	created := doRequest(t, f.server, http.MethodPost, "/api/v1/api-keys", map[string]string{"name": "toggle-key"}, cookie)
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

	disabled := doRequest(t, f.server, http.MethodPatch, "/api/v1/api-keys/"+result.Key.ID, map[string]bool{"enabled": false}, cookie)
	if disabled.Code != http.StatusNoContent {
		t.Fatalf("disable status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), result.Secret); err == nil {
		t.Fatal("disabled key should not authenticate")
	}
	listed := doRequest(t, f.server, http.MethodGet, "/api/v1/api-keys", nil, cookie)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"enabled":false`) {
		t.Fatalf("disabled key should remain listed: status=%d body=%s", listed.Code, listed.Body.String())
	}

	enabled := doRequest(t, f.server, http.MethodPatch, "/api/v1/api-keys/"+result.Key.ID, map[string]bool{"enabled": true}, cookie)
	if enabled.Code != http.StatusNoContent {
		t.Fatalf("enable status=%d body=%s", enabled.Code, enabled.Body.String())
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), result.Secret); err != nil {
		t.Fatalf("re-enabled key should authenticate: %v", err)
	}

	revoked := doRequest(t, f.server, http.MethodPost, "/api/v1/api-keys/"+result.Key.ID+"/revoke", nil, cookie)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	listed = doRequest(t, f.server, http.MethodGet, "/api/v1/api-keys", nil, cookie)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), result.Key.ID) {
		t.Fatalf("revoked key should be permanently removed: status=%d body=%s", listed.Code, listed.Body.String())
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), result.Secret); err == nil {
		t.Fatal("revoked key should not authenticate")
	}
}

func TestAPIKeyMutationValidation(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)

	missingEnabled := doRequest(t, f.server, http.MethodPatch, "/api/v1/api-keys/missing", map[string]string{}, cookie)
	if missingEnabled.Code != http.StatusBadRequest || !strings.Contains(missingEnabled.Body.String(), "enabled is required") {
		t.Fatalf("missing enabled status=%d body=%s", missingEnabled.Code, missingEnabled.Body.String())
	}
	missingKey := doRequest(t, f.server, http.MethodPatch, "/api/v1/api-keys/missing", map[string]bool{"enabled": false}, cookie)
	if missingKey.Code != http.StatusNotFound {
		t.Fatalf("missing key status=%d body=%s", missingKey.Code, missingKey.Body.String())
	}
	wrongMethod := doRequest(t, f.server, http.MethodGet, "/api/v1/api-keys/missing/revoke", nil, cookie)
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong revoke method status=%d body=%s", wrongMethod.Code, wrongMethod.Body.String())
	}
}
