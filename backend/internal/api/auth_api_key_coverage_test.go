package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	managersecurity "github.com/brantje/llamarack/backend/internal/security"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestAdminAuthAndAPIKeyClosedDatabaseErrors(t *testing.T) {
	db, err := database.Open(t.Context(), filepath.Join(t.TempDir(), "closed-auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.New(db, time.Hour)
	actor, err := authService.Bootstrap(t.Context(), "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	managerSettings := settings.New(db, settings.Defaults{
		SessionLifetime:   time.Hour,
		AllowedOrigins:    "https://manager.example.test",
		StartupTimeout:    time.Minute,
		AlwaysOnReconcile: time.Second,
	})
	network := managersecurity.NewNetwork(managerSettings)
	protector := managersecurity.NewLoginProtector(managerSettings)
	authHandler := NewAuthHandler(authService, network, protector, managerSettings)
	apiKeys := &apiKeysHandler{auth: authService}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	w := adminRequest(t, authHandler, http.MethodGet, "/api/v1/auth/bootstrap", nil, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("bootstrap closed-db status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, authHandler, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("login closed-db status=%d body=%s", w.Code, w.Body.String())
	}

	w = adminRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKeys.collection(w, r, actor)
	}), http.MethodGet, "/api/v1/api-keys", nil, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("api-key list closed-db status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKeys.collection(w, r, actor)
	}), http.MethodPost, "/api/v1/api-keys", map[string]string{"name": "test-key"}, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("api-key create closed-db status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKeys.item(w, r, actor, "key-id")
	}), http.MethodPatch, "/api/v1/api-keys/key-id", map[string]any{}, nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("api-key missing enabled status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestClearSessionCookies(t *testing.T) {
	for _, secure := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		ClearSessionCookies(recorder, secure)
		cookies := recorder.Result().Cookies()
		names := map[string]bool{}
		for _, cookie := range cookies {
			names[cookie.Name] = true
			if cookie.Value != "" || cookie.MaxAge != -1 || cookie.Secure != secure || cookie.Path != "/" {
				t.Fatalf("secure=%v cookie=%+v", secure, cookie)
			}
		}
		for _, name := range []string{sessionCookie, csrfCookie} {
			if !names[name] {
				t.Fatalf("secure=%v missing cookie %s in %+v", secure, name, cookies)
			}
		}
		if names["lcm_session"] || names["lcm_csrf"] {
			t.Fatalf("secure=%v previous cookie names still cleared: %+v", secure, cookies)
		}
	}
}
