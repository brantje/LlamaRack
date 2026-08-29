package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/database"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

func TestPhase10AuthAndAPIKeyClosedDatabaseErrors(t *testing.T) {
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
	authHandler := NewPhase10AuthHandler(authService, network, protector, managerSettings)
	apiKeys := &phase10APIKeysHandler{auth: authService}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	w := phase10Request(t, authHandler, http.MethodGet, "/api/v1/auth/bootstrap", nil, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("bootstrap closed-db status=%d body=%s", w.Code, w.Body.String())
	}
	w = phase10Request(t, authHandler, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("login closed-db status=%d body=%s", w.Code, w.Body.String())
	}

	w = phase10Request(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKeys.collection(w, r, actor)
	}), http.MethodGet, "/api/v1/api-keys", nil, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("api-key list closed-db status=%d body=%s", w.Code, w.Body.String())
	}
	w = phase10Request(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKeys.collection(w, r, actor)
	}), http.MethodPost, "/api/v1/api-keys", map[string]string{"name": "test-key"}, nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("api-key create closed-db status=%d body=%s", w.Code, w.Body.String())
	}
	w = phase10Request(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKeys.item(w, r, actor, "key-id")
	}), http.MethodPatch, "/api/v1/api-keys/key-id", map[string]any{}, nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("api-key missing enabled status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestClearLegacySessionCookies(t *testing.T) {
	for _, secure := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		ClearSessionCookies(recorder, secure)
		cookies := recorder.Result().Cookies()
		if len(cookies) != 2 {
			t.Fatalf("secure=%v cookies=%+v", secure, cookies)
		}
		for _, cookie := range cookies {
			if cookie.Value != "" || cookie.MaxAge != -1 || cookie.Secure != secure || cookie.Path != "/" {
				t.Fatalf("secure=%v cookie=%+v", secure, cookie)
			}
		}
	}
}
