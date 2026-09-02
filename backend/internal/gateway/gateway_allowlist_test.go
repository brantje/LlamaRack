package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/auth"
)

func TestGatewayRejectsManagementKeysAndFiltersAllowlist(t *testing.T) {
	f := newGatewayFixture(t, false)
	ctx := t.Context()
	ownerID := f.ownerID

	_, managementSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "mgmt", KeyType: auth.APIKeyTypeManagement, OwnerUserID: &ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", managementSecret, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "management_key_not_allowed") {
		t.Fatalf("management on /v1=%d %s", w.Code, w.Body.String())
	}

	login, err := f.gateway.auth.LoginBearerWithMetadata(ctx, "admin", "correct-horse-battery", "", "")
	if err != nil {
		t.Fatal(err)
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", login.AccessToken, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("JWT on /v1=%d %s", w.Code, w.Body.String())
	}

	_, allowedSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "allow", KeyType: auth.APIKeyTypeInference, OwnerUserID: &ownerID, InstanceIDs: []string{"gateway-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", allowedSecret, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gateway-model") {
		t.Fatalf("allowlist list=%d %s", w.Code, w.Body.String())
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models/gateway-model", allowedSecret, "")
	if w.Code != http.StatusOK {
		t.Fatalf("allowlist get model=%d %s", w.Code, w.Body.String())
	}
	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", allowedSecret, `{"model":"other"}`)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "instance_not_allowed") {
		t.Fatalf("allowlist miss=%d %s", w.Code, w.Body.String())
	}

	var modelID string
	if err := f.db.QueryRowContext(ctx, `SELECT model_id FROM instances WHERE id=?`, "gateway-model").Scan(&modelID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO instances(id,model_id,name) VALUES('temp-only',?,'Temp')`, modelID); err != nil {
		t.Fatal(err)
	}
	stale, staleSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "stale", KeyType: auth.APIKeyTypeInference, OwnerUserID: &ownerID, InstanceIDs: []string{"temp-only"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM instances WHERE id=?`, "temp-only"); err != nil {
		t.Fatal(err)
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", staleSecret, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "api_key_instances_unavailable") {
		t.Fatalf("all-stale=%d %s key=%s", w.Code, w.Body.String(), stale.ID)
	}

	_, fullSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "full", KeyType: auth.APIKeyTypeFull, OwnerUserID: &ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", fullSecret, "")
	if w.Code != http.StatusOK {
		t.Fatalf("full key list=%d %s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	data, _ := payload["data"].([]any)
	if len(data) == 0 {
		t.Fatalf("full key should see models: %s", w.Body.String())
	}
}
