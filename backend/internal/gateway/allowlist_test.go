package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/instances"
)

func TestGatewayTypedKeysAllowlistAndJWTRejection(t *testing.T) {
	f := newGatewayFixture(t, false)
	ctx := context.Background()
	users, err := f.gateway.auth.ListUsers(ctx)
	if err != nil || len(users) != 1 {
		t.Fatalf("users=%+v err=%v", users, err)
	}
	ownerID := users[0].ID
	existing, err := f.lifecycle.Instances().GetBySlug(ctx, "gateway-model")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.lifecycle.Instances().Create(ctx, instances.CreateInput{ModelID: existing.ModelID, Name: "Other model", Slug: "other-model"})
	if err != nil {
		t.Fatal(err)
	}

	_, managementSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{Name: "mgmt", KeyType: auth.APIKeyTypeManagement, OwnerUserID: &ownerID})
	if err != nil {
		t.Fatal(err)
	}
	_, fullSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{Name: "full", KeyType: auth.APIKeyTypeFull, OwnerUserID: &ownerID})
	if err != nil {
		t.Fatal(err)
	}
	_, scopedSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{Name: "scoped", KeyType: auth.APIKeyTypeInference, OwnerUserID: &ownerID, InstanceIDs: []string{existing.ID}})
	if err != nil {
		t.Fatal(err)
	}
	stale, staleSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{Name: "stale", KeyType: auth.APIKeyTypeInference, OwnerUserID: &ownerID, InstanceIDs: []string{other.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if stale.ID == "" {
		t.Fatal("expected stale key")
	}
	login, err := f.gateway.auth.LoginBearerWithMetadata(ctx, "admin", "correct-horse-battery", "127.0.0.1", "gateway-allowlist")
	if err != nil {
		t.Fatal(err)
	}

	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", managementSecret, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "management_key_not_allowed") {
		t.Fatalf("management on /v1=%d %s", w.Code, w.Body.String())
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", login.AccessToken, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("JWT on /v1=%d %s", w.Code, w.Body.String())
	}

	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", scopedSecret, "")
	if w.Code != http.StatusOK {
		t.Fatalf("scoped list=%d %s", w.Code, w.Body.String())
	}
	ids := modelIDs(t, w.Body.Bytes())
	if len(ids) != 1 || ids[0] != "gateway-model" {
		t.Fatalf("scoped models=%v", ids)
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models/other-model", scopedSecret, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "instance_not_allowed") {
		t.Fatalf("scoped get other=%d %s", w.Code, w.Body.String())
	}

	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", fullSecret, "")
	if w.Code != http.StatusOK {
		t.Fatalf("full list=%d %s", w.Code, w.Body.String())
	}
	ids = modelIDs(t, w.Body.Bytes())
	if len(ids) != 2 {
		t.Fatalf("full models=%v", ids)
	}

	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", f.secret, "")
	if w.Code != http.StatusOK {
		t.Fatalf("unscoped inference list=%d %s", w.Code, w.Body.String())
	}
	if got := modelIDs(t, w.Body.Bytes()); len(got) != 2 {
		t.Fatalf("empty allowlist should list all enabled instances: %v", got)
	}

	if err := f.lifecycle.Instances().Delete(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", staleSecret, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "api_key_instances_unavailable") {
		t.Fatalf("all-stale list=%d %s", w.Code, w.Body.String())
	}
	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", staleSecret, `{"model":"gateway-model"}`)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "api_key_instances_unavailable") {
		t.Fatalf("all-stale chat=%d %s", w.Code, w.Body.String())
	}
}

func modelIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode models: %v body=%s", err, body)
	}
	ids := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		ids = append(ids, item.ID)
	}
	return ids
}
