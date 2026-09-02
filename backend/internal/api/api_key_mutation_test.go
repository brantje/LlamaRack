package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
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
		ctx := context.WithValue(r.Context(), managementAuthContextKey{}, managementAuthContext{User: &user, Session: &session})
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestAPIKeyLifecycleDisableAndInPlaceRotate(t *testing.T) {
	f := newAPIFixture(t, nil)
	handler := apiKeyHandler(t, f)
	user, err := f.auth.UserByID(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}

	created := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys", map[string]any{"name": "toggle-key", "owner_user_id": user.ID}, nil)
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
	if result.Key.ID == "" || !strings.HasPrefix(result.Secret, "sk-") || result.Key.KeyType != auth.APIKeyTypeInference || result.Key.Status != auth.APIKeyStatusEnabled {
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

	listed := doRequest(t, handler, http.MethodGet, "/api/v1/api-keys", nil, nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), result.Key.ID) || strings.Contains(listed.Body.String(), `"revoked_at"`) {
		t.Fatalf("listed keys: status=%d body=%s", listed.Code, listed.Body.String())
	}
}

func TestAPIKeyRotationKeepsIDAndReturnsNewSecretOnce(t *testing.T) {
	f := newAPIFixture(t, nil)
	handler := apiKeyHandler(t, f)
	user, err := f.auth.UserByID(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}

	created := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys", map[string]any{"name": "rotate-key", "owner_user_id": user.ID}, nil)
	var original struct {
		Key    auth.APIKey `json:"key"`
		Secret string      `json:"secret"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}

	rotated := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys/"+original.Key.ID+"/rotate", nil, nil)
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotate status=%d body=%s", rotated.Code, rotated.Body.String())
	}
	var replacement struct {
		Key    auth.APIKey `json:"key"`
		Secret string      `json:"secret"`
	}
	if err := json.Unmarshal(rotated.Body.Bytes(), &replacement); err != nil {
		t.Fatal(err)
	}
	if replacement.Key.ID != original.Key.ID || replacement.Secret == "" || replacement.Secret == original.Secret {
		t.Fatalf("invalid replacement: %+v", replacement)
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), original.Secret); err == nil {
		t.Fatal("rotated original secret should fail immediately")
	}
	if err := f.auth.AuthenticateAPIKey(t.Context(), replacement.Secret); err != nil {
		t.Fatalf("replacement secret should authenticate: %v", err)
	}
	listed := doRequest(t, handler, http.MethodGet, "/api/v1/api-keys", nil, nil)
	if listed.Code != http.StatusOK || strings.Count(listed.Body.String(), original.Key.ID) != 1 || strings.Contains(listed.Body.String(), replacement.Secret) {
		t.Fatalf("rotation contract violated: %s", listed.Body.String())
	}
}

func TestAPIKeyMutationValidation(t *testing.T) {
	f := newAPIFixture(t, nil)
	handler := apiKeyHandler(t, f)
	user, err := f.auth.UserByID(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}

	unauthorized := doRequest(t, NewAPIKeysHandler(f.auth), http.MethodGet, "/api/v1/api-keys", nil, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized list=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	blank := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys", map[string]string{"name": "  "}, nil)
	if blank.Code != http.StatusBadRequest || !strings.Contains(blank.Body.String(), "name is required") {
		t.Fatalf("blank name status=%d body=%s", blank.Code, blank.Body.String())
	}
	missingKey := doRequest(t, handler, http.MethodPatch, "/api/v1/api-keys/missing", map[string]bool{"enabled": false}, nil)
	if missingKey.Code != http.StatusNotFound {
		t.Fatalf("missing key status=%d body=%s", missingKey.Code, missingKey.Body.String())
	}
	missingOwner := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys", map[string]string{"name": "no-owner"}, nil)
	if missingOwner.Code != http.StatusBadRequest || !strings.Contains(missingOwner.Body.String(), "exactly one") {
		t.Fatalf("missing owner status=%d body=%s", missingOwner.Code, missingOwner.Body.String())
	}
	wrongMethod := doRequest(t, handler, http.MethodGet, "/api/v1/api-keys/missing/rotate", nil, nil)
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong rotate method status=%d body=%s", wrongMethod.Code, wrongMethod.Body.String())
	}
	revokedPath := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys/missing/revoke", nil, nil)
	if revokedPath.Code != http.StatusNotFound {
		t.Fatalf("revoke path status=%d body=%s", revokedPath.Code, revokedPath.Body.String())
	}
	badPath := doRequest(t, handler, http.MethodGet, "/api/v1/api-keys/a/b/c", nil, nil)
	if badPath.Code != http.StatusNotFound {
		t.Fatalf("bad path status=%d body=%s", badPath.Code, badPath.Body.String())
	}
	emptyItem := doRequest(t, handler, http.MethodGet, "/api/v1/api-keys//", nil, nil)
	if emptyItem.Code != http.StatusNotFound {
		t.Fatalf("empty item path status=%d body=%s", emptyItem.Code, emptyItem.Body.String())
	}

	account, err := f.auth.CreateServiceAccount(t.Context(), "docs", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format(time.DateOnly)
	owned := doRequest(t, handler, http.MethodPost, "/api/v1/api-keys", map[string]any{
		"name": "sa-owned", "key_type": "full", "owner_service_account_id": account.ID, "expires_on": tomorrow,
	}, nil)
	if owned.Code != http.StatusCreated || !strings.Contains(owned.Body.String(), `"owner_kind":"service_account"`) {
		t.Fatalf("SA-owned create status=%d body=%s", owned.Code, owned.Body.String())
	}
	var created struct {
		Key auth.APIKey `json:"key"`
	}
	if err := json.Unmarshal(owned.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	cleared := doRequest(t, handler, http.MethodPatch, "/api/v1/api-keys/"+created.Key.ID, map[string]any{"expires_on": nil}, nil)
	if cleared.Code != http.StatusNoContent {
		t.Fatalf("clear expiry status=%d body=%s", cleared.Code, cleared.Body.String())
	}
	setExpiry := doRequest(t, handler, http.MethodPatch, "/api/v1/api-keys/"+created.Key.ID, map[string]any{"expires_on": tomorrow}, nil)
	if setExpiry.Code != http.StatusNoContent {
		t.Fatalf("set expiry status=%d body=%s", setExpiry.Code, setExpiry.Body.String())
	}
	invalidExpiry := doRequest(t, handler, http.MethodPatch, "/api/v1/api-keys/"+created.Key.ID, []byte(`{"expires_on":{"day":1}}`), nil)
	if invalidExpiry.Code != http.StatusBadRequest {
		t.Fatalf("invalid expiry status=%d body=%s", invalidExpiry.Code, invalidExpiry.Body.String())
	}
	reassign := doRequest(t, handler, http.MethodPatch, "/api/v1/api-keys/"+created.Key.ID, map[string]any{"owner_user_id": user.ID}, nil)
	if reassign.Code != http.StatusNoContent {
		t.Fatalf("reassign owner status=%d body=%s", reassign.Code, reassign.Body.String())
	}
}
