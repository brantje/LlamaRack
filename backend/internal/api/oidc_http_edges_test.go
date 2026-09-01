package api

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	managersecurity "github.com/brantje/llamarack/backend/internal/security"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestOIDCAdditionalHTTPBranches(t *testing.T) {
	f := newAPIOIDCFixture(t)

	for _, tc := range []struct {
		method string
		path   string
		body   any
		status int
	}{
		{http.MethodGet, "/api/v1/me/identities", nil, http.StatusUnauthorized},
		{http.MethodPost, "/api/v1/admin/auth/settings", nil, http.StatusMethodNotAllowed},
		{http.MethodPut, "/api/v1/admin/auth/settings", "{", http.StatusBadRequest},
		{http.MethodGet, "/api/v1/auth/oidc/missing/start", nil, http.StatusBadRequest},
		{http.MethodGet, "/api/v1/auth/oidc//start", nil, http.StatusNotFound},
		{http.MethodPost, "/api/v1/auth/oidc/exchange", "{", http.StatusBadRequest},
		{http.MethodPost, "/api/v1/auth/ws-ticket", nil, http.StatusUnauthorized},
		{http.MethodPost, "/api/v1/admin/auth/providers", map[string]any{"name": ""}, http.StatusBadRequest},
		{http.MethodPost, "/api/v1/admin/auth/providers/missing/test", nil, http.StatusBadRequest},
		{http.MethodGet, "/api/v1/admin/auth/providers/missing", nil, http.StatusNotFound},
		{http.MethodPut, "/api/v1/admin/auth/providers/missing", f.providerInput("Missing"), http.StatusNotFound},
		{http.MethodDelete, "/api/v1/admin/auth/providers/missing", nil, http.StatusNotFound},
		{http.MethodPost, "/api/v1/admin/auth/providers/missing", nil, http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v1/admin/auth/providers/missing/nope", nil, http.StatusNotFound},
		{http.MethodPost, "/api/v1/admin/auth/identities", map[string]any{"user_id": 0}, http.StatusBadRequest},
		{http.MethodPut, "/api/v1/admin/auth/identities", nil, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/admin/auth/identities/missing", nil, http.StatusMethodNotAllowed},
		{http.MethodDelete, "/api/v1/admin/auth/identities/missing", nil, http.StatusNotFound},
	} {
		w := adminRequest(t, f.raw, tc.method, tc.path, tc.body, nil, nil)
		if w.Code != tc.status {
			t.Fatalf("%s %s status=%d want=%d body=%s", tc.method, tc.path, w.Code, tc.status, w.Body.String())
		}
	}
}

func TestOIDCClosedDatabaseErrors(t *testing.T) {
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "closed-oidc.db"))
	if err != nil {
		t.Fatal(err)
	}
	managerSettings := settings.New(db, settings.Defaults{
		SessionLifetime:   time.Hour,
		AllowedOrigins:    "https://manager.example.test",
		StartupTimeout:    time.Minute,
		AlwaysOnReconcile: time.Second,
	})
	authService := auth.New(db, time.Hour)
	secrets := &apiOIDCSecrets{data: map[string]string{}}
	oidcManager := auth.NewOIDCManager(authService, managerSettings, secrets)
	network := managersecurity.NewNetwork(managerSettings)
	h := NewOIDCHandler(oidcManager, authService, managerSettings, network)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/v1/auth/providers", "/api/v1/admin/auth/settings"} {
		w := adminRequest(t, h, http.MethodGet, path, nil, nil, nil)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("GET %s status=%d want=%d body=%s", path, w.Code, http.StatusInternalServerError, w.Body.String())
		}
	}
}
