package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
)

func TestAPIKeyEnableDisableDeleteAndRevokeRoutes(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)

	created := doRequest(t, f.server, http.MethodPost, "/api/v1/api-keys", map[string]string{"name": "sdk"}, cookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var payload struct {
		Key    auth.APIKey `json:"key"`
		Secret string      `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Key.ID == "" || payload.Secret == "" {
		t.Fatalf("created payload=%+v", payload)
	}

	disabled := doRequest(t, f.server, http.MethodPatch, "/api/v1/api-keys/"+payload.Key.ID, map[string]bool{"enabled": false}, cookie)
	if disabled.Code != http.StatusNoContent {
		t.Fatalf("disable status=%d body=%s", disabled.Code, disabled.Body.String())
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), payload.Secret); err == nil {
		t.Fatal("disabled key should not authenticate")
	}
	listed := doRequest(t, f.server, http.MethodGet, "/api/v1/api-keys", nil, cookie)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"enabled":false`) {
		t.Fatalf("disabled list status=%d body=%s", listed.Code, listed.Body.String())
	}

	enabled := doRequest(t, f.server, http.MethodPatch, "/api/v1/api-keys/"+payload.Key.ID, map[string]bool{"enabled": true}, cookie)
	if enabled.Code != http.StatusNoContent {
		t.Fatalf("enable status=%d body=%s", enabled.Code, enabled.Body.String())
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), payload.Secret); err != nil {
		t.Fatalf("re-enabled key should authenticate: %v", err)
	}

	missingEnabled := doRequest(t, f.server, http.MethodPatch, "/api/v1/api-keys/missing", map[string]bool{"enabled": true}, cookie)
	if missingEnabled.Code != http.StatusNotFound {
		t.Fatalf("missing enable status=%d body=%s", missingEnabled.Code, missingEnabled.Body.String())
	}
	missingBody := doRequest(t, f.server, http.MethodPatch, "/api/v1/api-keys/"+payload.Key.ID, map[string]string{}, cookie)
	if missingBody.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled field status=%d body=%s", missingBody.Code, missingBody.Body.String())
	}

	deleted := doRequest(t, f.server, http.MethodDelete, "/api/v1/api-keys/"+payload.Key.ID, nil, cookie)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	listed = doRequest(t, f.server, http.MethodGet, "/api/v1/api-keys", nil, cookie)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), payload.Key.ID) {
		t.Fatalf("deleted key still listed status=%d body=%s", listed.Code, listed.Body.String())
	}
	missingDelete := doRequest(t, f.server, http.MethodDelete, "/api/v1/api-keys/"+payload.Key.ID, nil, cookie)
	if missingDelete.Code != http.StatusNotFound {
		t.Fatalf("missing delete status=%d body=%s", missingDelete.Code, missingDelete.Body.String())
	}

	second, _, err := f.auth.CreateAPIKey(t.Context(), "legacy-revoke")
	if err != nil {
		t.Fatal(err)
	}
	revoked := doRequest(t, f.server, http.MethodPost, "/api/v1/api-keys/"+second.ID+"/revoke", nil, cookie)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", revoked.Code, revoked.Body.String())
	}
	listed = doRequest(t, f.server, http.MethodGet, "/api/v1/api-keys", nil, cookie)
	if strings.Contains(listed.Body.String(), second.ID) {
		t.Fatalf("revoked key should be removed: %s", listed.Body.String())
	}

	wrongMethod := doRequest(t, f.server, http.MethodGet, "/api/v1/api-keys/whatever", nil, cookie)
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status=%d", wrongMethod.Code)
	}
	badPath := doRequest(t, f.server, http.MethodDelete, "/api/v1/api-keys/a/b/c", nil, cookie)
	if badPath.Code != http.StatusNotFound {
		t.Fatalf("bad path status=%d", badPath.Code)
	}
}
