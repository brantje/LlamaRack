package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
)

func TestManagementSecurityAPIKeyPlanesAndDenylist(t *testing.T) {
	fixture := newAuthSecurityFixture(t)
	admin, err := fixture.auth.Bootstrap(t.Context(), "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/api-keys", NewAPIKeysHandler(fixture.auth))
	mux.Handle("/api/v1/api-keys/", NewAPIKeysHandler(fixture.auth))
	mux.Handle("/api/v1/admin/service-accounts", NewServiceAccountsHandler(fixture.auth))
	mux.Handle("/api/v1/admin/service-accounts/", NewServiceAccountsHandler(fixture.auth))
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/v1/playground/chat/completions", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/v1/auth/logout", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/api/v1/auth/ws-ticket", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	handler := ManagementSecurity(fixture.auth, fixture.network, mux)

	inference, inferenceSecret, err := fixture.auth.CreateAPIKey(t.Context(), auth.CreateAPIKeyInput{Name: "inf", KeyType: auth.APIKeyTypeInference, OwnerUserID: &admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	management, managementSecret, err := fixture.auth.CreateAPIKey(t.Context(), auth.CreateAPIKeyInput{Name: "mgmt", KeyType: auth.APIKeyTypeManagement, OwnerUserID: &admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, fullSecret, err := fixture.auth.CreateAPIKey(t.Context(), auth.CreateAPIKeyInput{Name: "full", KeyType: auth.APIKeyTypeFull, OwnerUserID: &admin.ID})
	if err != nil {
		t.Fatal(err)
	}
	account, err := fixture.auth.CreateServiceAccount(t.Context(), "bots", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, saSecret, err := fixture.auth.CreateAPIKey(t.Context(), auth.CreateAPIKeyInput{Name: "sa", KeyType: auth.APIKeyTypeFull, OwnerServiceAccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, saMgmtSecret, err := fixture.auth.CreateAPIKey(t.Context(), auth.CreateAPIKeyInput{Name: "sa-mgmt", KeyType: auth.APIKeyTypeManagement, OwnerServiceAccountID: account.ID})
	if err != nil {
		t.Fatal(err)
	}
	login, err := fixture.auth.LoginBearerWithMetadata(t.Context(), "admin", "correct-horse-battery", "192.0.2.10", "principal-test")
	if err != nil {
		t.Fatal(err)
	}

	w := adminRequest(t, handler, http.MethodGet, "/api/v1/api-keys", nil, nil, map[string]string{"Authorization": "Bearer " + inferenceSecret})
	if w.Code != http.StatusForbidden {
		t.Fatalf("inference on management=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts", nil, nil, map[string]string{"Authorization": "Bearer " + inferenceSecret})
	if w.Code != http.StatusForbidden {
		t.Fatalf("inference on SA admin=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodGet, "/api/v1/api-keys", nil, nil, map[string]string{"Authorization": "Bearer " + managementSecret})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), inference.ID) || !strings.Contains(w.Body.String(), management.ID) {
		t.Fatalf("management key list=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodGet, "/api/v1/me", nil, nil, map[string]string{"Authorization": "Bearer " + managementSecret})
	if w.Code != http.StatusForbidden {
		t.Fatalf("key on /me=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodPost, "/api/v1/auth/logout", nil, nil, map[string]string{"Authorization": "Bearer " + managementSecret})
	if w.Code != http.StatusForbidden {
		t.Fatalf("key on logout=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodPost, "/api/v1/auth/ws-ticket", nil, nil, map[string]string{"Authorization": "Bearer " + managementSecret})
	if w.Code != http.StatusForbidden {
		t.Fatalf("key on ws-ticket=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodPost, "/api/v1/playground/chat/completions", nil, nil, map[string]string{"Authorization": "Bearer " + managementSecret})
	if w.Code != http.StatusForbidden {
		t.Fatalf("key on playground=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodGet, "/api/v1/logs/stream", nil, nil, map[string]string{"Authorization": "Bearer " + managementSecret})
	if w.Code != http.StatusForbidden {
		t.Fatalf("key on logs stream=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodGet, "/api/v1/api-keys", nil, nil, map[string]string{"Authorization": "Bearer sk-bogusvalue"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unknown sk- key=%d body=%s", w.Code, w.Body.String())
	}
	for _, secret := range []string{managementSecret, saMgmtSecret} {
		w = adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts", nil, nil, map[string]string{"Authorization": "Bearer " + secret})
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "this API key cannot manage service accounts") {
			t.Fatalf("management key on SA admin=%d body=%s", w.Code, w.Body.String())
		}
		w = adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts/"+account.ID, nil, nil, map[string]string{"Authorization": "Bearer " + secret})
		if w.Code != http.StatusForbidden {
			t.Fatalf("management key on SA item=%d body=%s", w.Code, w.Body.String())
		}
	}
	createdSA := adminRequest(t, handler, http.MethodPost, "/api/v1/admin/service-accounts", map[string]string{"name": "from-mgmt-key"}, nil, map[string]string{"Authorization": "Bearer " + managementSecret})
	if createdSA.Code != http.StatusForbidden {
		t.Fatalf("management key create SA=%d body=%s", createdSA.Code, createdSA.Body.String())
	}
	for _, tc := range []struct{ name, secret string }{{"user-owned full", fullSecret}, {"SA-owned full", saSecret}} {
		w = adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts", nil, nil, map[string]string{"Authorization": "Bearer " + tc.secret})
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), account.ID) {
			t.Fatalf("%s on SA admin=%d body=%s", tc.name, w.Code, w.Body.String())
		}
		w = adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts/"+account.ID, nil, nil, map[string]string{"Authorization": "Bearer " + tc.secret})
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"keys"`) {
			t.Fatalf("%s on SA item=%d body=%s", tc.name, w.Code, w.Body.String())
		}
	}
	createdFull := adminRequest(t, handler, http.MethodPost, "/api/v1/admin/service-accounts", map[string]string{"name": "from-full-key"}, nil, map[string]string{"Authorization": "Bearer " + fullSecret})
	if createdFull.Code != http.StatusCreated || !strings.Contains(createdFull.Body.String(), "from-full-key") {
		t.Fatalf("user-owned full key create SA=%d body=%s", createdFull.Code, createdFull.Body.String())
	}
	createdSAOwned := adminRequest(t, handler, http.MethodPost, "/api/v1/admin/service-accounts", map[string]string{"name": "from-sa-full-key"}, nil, map[string]string{"Authorization": "Bearer " + saSecret})
	if createdSAOwned.Code != http.StatusCreated || !strings.Contains(createdSAOwned.Body.String(), "from-sa-full-key") {
		t.Fatalf("SA-owned full key create SA=%d body=%s", createdSAOwned.Code, createdSAOwned.Body.String())
	}
	var createdFullAccount auth.ServiceAccount
	decodeAPIJSON(t, createdFull, &createdFullAccount)
	patchedFull := adminRequest(t, handler, http.MethodPatch, "/api/v1/admin/service-accounts/"+createdFullAccount.ID, map[string]any{"name": "from-full-key-renamed"}, nil, map[string]string{"Authorization": "Bearer " + fullSecret})
	if patchedFull.Code != http.StatusNoContent {
		t.Fatalf("user-owned full key patch SA=%d body=%s", patchedFull.Code, patchedFull.Body.String())
	}
	var createdSAOwnedAccount auth.ServiceAccount
	decodeAPIJSON(t, createdSAOwned, &createdSAOwnedAccount)
	deletedSAOwned := adminRequest(t, handler, http.MethodDelete, "/api/v1/admin/service-accounts/"+createdSAOwnedAccount.ID, nil, nil, map[string]string{"Authorization": "Bearer " + saSecret})
	if deletedSAOwned.Code != http.StatusNoContent {
		t.Fatalf("SA-owned full key delete SA=%d body=%s", deletedSAOwned.Code, deletedSAOwned.Body.String())
	}
	w = adminRequest(t, handler, http.MethodGet, "/api/v1/api-keys", nil, nil, map[string]string{"Authorization": "Bearer " + saSecret})
	if w.Code != http.StatusOK {
		t.Fatalf("SA key on api-keys=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodGet, "/api/v1/api-keys", nil, nil, map[string]string{"Authorization": "Bearer " + fullSecret})
	if w.Code != http.StatusOK {
		t.Fatalf("user-owned full key on api-keys=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts", nil, nil, map[string]string{"Authorization": "Bearer " + login.AccessToken})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), account.ID) {
		t.Fatalf("JWT on SA admin=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, handler, http.MethodGet, "/api/v1/api-keys", nil, nil, map[string]string{"Authorization": "Bearer not-a-key"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid bearer=%d body=%s", w.Code, w.Body.String())
	}
}

func TestServiceAccountHTTPCRUD(t *testing.T) {
	fixture := newAuthSecurityFixture(t)
	if _, err := fixture.auth.Bootstrap(t.Context(), "admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	login, err := fixture.auth.LoginBearerWithMetadata(t.Context(), "admin", "correct-horse-battery", "192.0.2.10", "sa-test")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/admin/service-accounts", NewServiceAccountsHandler(fixture.auth))
	mux.Handle("/api/v1/admin/service-accounts/", NewServiceAccountsHandler(fixture.auth))
	handler := ManagementSecurity(fixture.auth, fixture.network, mux)
	headers := map[string]string{"Authorization": "Bearer " + login.AccessToken}

	created := adminRequest(t, handler, http.MethodPost, "/api/v1/admin/service-accounts", map[string]string{"name": "ci"}, nil, headers)
	if created.Code != http.StatusCreated {
		t.Fatalf("create SA status=%d body=%s", created.Code, created.Body.String())
	}
	var account auth.ServiceAccount
	decodeAPIJSON(t, created, &account)
	if account.ID == "" {
		t.Fatalf("create account=%+v", account)
	}
	listed := adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts", nil, nil, headers)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), account.ID) {
		t.Fatalf("list SA=%d %s", listed.Code, listed.Body.String())
	}
	patched := adminRequest(t, handler, http.MethodPatch, "/api/v1/admin/service-accounts/"+account.ID, map[string]any{"name": "ci-bots", "enabled": false}, nil, headers)
	if patched.Code != http.StatusNoContent {
		t.Fatalf("patch SA=%d %s", patched.Code, patched.Body.String())
	}
	got := adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts/"+account.ID, nil, nil, headers)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "ci-bots") || !strings.Contains(got.Body.String(), `"enabled":false`) {
		t.Fatalf("get SA=%d %s", got.Code, got.Body.String())
	}
	deleted := adminRequest(t, handler, http.MethodDelete, "/api/v1/admin/service-accounts/"+account.ID, nil, nil, headers)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete SA=%d %s", deleted.Code, deleted.Body.String())
	}
	missing := adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts/"+account.ID, nil, nil, headers)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("deleted get=%d %s", missing.Code, missing.Body.String())
	}
	blank := adminRequest(t, handler, http.MethodPost, "/api/v1/admin/service-accounts", map[string]string{"name": " "}, nil, headers)
	if blank.Code != http.StatusBadRequest {
		t.Fatalf("blank SA name=%d %s", blank.Code, blank.Body.String())
	}
	unauthorized := adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts", nil, nil, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated SA list=%d %s", unauthorized.Code, unauthorized.Body.String())
	}
	nested := adminRequest(t, handler, http.MethodGet, "/api/v1/admin/service-accounts/"+account.ID+"/keys", nil, nil, headers)
	if nested.Code != http.StatusNotFound {
		t.Fatalf("nested SA path=%d %s", nested.Code, nested.Body.String())
	}
	wrong := adminRequest(t, handler, http.MethodPut, "/api/v1/admin/service-accounts", nil, nil, headers)
	if wrong.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong collection method=%d", wrong.Code)
	}
	itemMethod := adminRequest(t, handler, http.MethodPut, "/api/v1/admin/service-accounts/"+account.ID, nil, nil, headers)
	if itemMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong item method=%d", itemMethod.Code)
	}
	missingPatch := adminRequest(t, handler, http.MethodPatch, "/api/v1/admin/service-accounts/missing", map[string]any{"name": "gone"}, nil, headers)
	if missingPatch.Code != http.StatusNotFound {
		t.Fatalf("missing patch=%d %s", missingPatch.Code, missingPatch.Body.String())
	}
	missingDelete := adminRequest(t, handler, http.MethodDelete, "/api/v1/admin/service-accounts/missing", nil, nil, headers)
	if missingDelete.Code != http.StatusNotFound {
		t.Fatalf("missing delete=%d %s", missingDelete.Code, missingDelete.Body.String())
	}
	createdTwo := adminRequest(t, handler, http.MethodPost, "/api/v1/admin/service-accounts", map[string]string{"name": "second"}, nil, headers)
	if createdTwo.Code != http.StatusCreated {
		t.Fatalf("create second SA=%d %s", createdTwo.Code, createdTwo.Body.String())
	}
	var second auth.ServiceAccount
	decodeAPIJSON(t, createdTwo, &second)
	enabledTrue := adminRequest(t, handler, http.MethodPatch, "/api/v1/admin/service-accounts/"+second.ID, map[string]any{"enabled": true}, nil, headers)
	if enabledTrue.Code != http.StatusNoContent {
		t.Fatalf("enable SA=%d %s", enabledTrue.Code, enabledTrue.Body.String())
	}

	badJSON := httptest.NewRequest(http.MethodPost, "/api/v1/admin/service-accounts", strings.NewReader("{"))
	badJSON.Header.Set("Authorization", headers["Authorization"])
	badJSON.Header.Set("Content-Type", "application/json")
	badPost := httptest.NewRecorder()
	handler.ServeHTTP(badPost, badJSON)
	if badPost.Code != http.StatusBadRequest {
		t.Fatalf("bad create JSON=%d %s", badPost.Code, badPost.Body.String())
	}
	badPatchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/service-accounts/"+second.ID, strings.NewReader("{"))
	badPatchReq.Header.Set("Authorization", headers["Authorization"])
	badPatchReq.Header.Set("Content-Type", "application/json")
	badPatch := httptest.NewRecorder()
	handler.ServeHTTP(badPatch, badPatchReq)
	if badPatch.Code != http.StatusBadRequest {
		t.Fatalf("bad patch JSON=%d %s", badPatch.Code, badPatch.Body.String())
	}

	unauthedHandler := NewServiceAccountsHandler(fixture.auth)
	unauthed := doRequest(t, unauthedHandler, http.MethodGet, "/api/v1/admin/service-accounts", nil, nil)
	if unauthed.Code != http.StatusUnauthorized {
		t.Fatalf("handler without principal=%d %s", unauthed.Code, unauthed.Body.String())
	}
}

func TestServiceAccountHandlerClosedDatabase(t *testing.T) {
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "sa-closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.New(db, time.Hour)
	actor, err := authService.Bootstrap(t.Context(), "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	account, err := authService.CreateServiceAccount(t.Context(), "closed", actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), managementAuthContextKey{}, managementAuthContext{User: &actor})
		NewServiceAccountsHandler(authService).ServeHTTP(w, r.WithContext(ctx))
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"list", http.MethodGet, "/api/v1/admin/service-accounts", nil},
		{"create", http.MethodPost, "/api/v1/admin/service-accounts", map[string]string{"name": "next"}},
		{"get", http.MethodGet, "/api/v1/admin/service-accounts/" + account.ID, nil},
		{"patch", http.MethodPatch, "/api/v1/admin/service-accounts/" + account.ID, map[string]any{"name": "renamed"}},
		{"delete", http.MethodDelete, "/api/v1/admin/service-accounts/" + account.ID, nil},
	} {
		w := doRequest(t, handler, tc.method, tc.path, tc.body, nil)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s status=%d body=%s", tc.name, w.Code, w.Body.String())
		}
	}
}
