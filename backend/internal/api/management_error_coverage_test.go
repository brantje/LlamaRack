package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

func TestAdminManagementClosedDatabaseErrorPaths(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(t.Context(), filepath.Join(root, "closed-admin.db"))
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
		DataDir:           root,
	})
	secrets, err := huggingface.NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	network := managersecurity.NewNetwork(managerSettings)
	h := &adminHandler{
		auth: authService,
		settings: managerSettings,
		secrets: secrets,
		network: network,
		profile: func() (llamacpp.Profile, error) { return llamacpp.Profile{}, errors.New("unavailable") },
		started: time.Now(),
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	request := func(method, path string, body any, invoke func(*httptest.ResponseRecorder, *http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		base := adminRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorder, ok := w.(*httptest.ResponseRecorder)
			if !ok {
				t.Fatal("expected response recorder")
			}
			invoke(recorder, r)
		}), method, path, body, nil, nil)
		return base
	}

	w := request(http.MethodGet, "/api/v1/users", nil, func(w *httptest.ResponseRecorder, r *http.Request) { h.users(w, r, actor) })
	if w.Code != http.StatusInternalServerError { t.Fatalf("users status=%d body=%s", w.Code, w.Body.String()) }

	w = request(http.MethodGet, "/api/v1/me/sessions", nil, func(w *httptest.ResponseRecorder, r *http.Request) { h.listSessions(w, r, actor.ID, "current") })
	if w.Code != http.StatusInternalServerError { t.Fatalf("sessions status=%d body=%s", w.Code, w.Body.String()) }

	w = request(http.MethodDelete, "/api/v1/sessions/session", nil, func(w *httptest.ResponseRecorder, r *http.Request) { h.sessionRoute(w, r, actor, "session") })
	if w.Code != http.StatusInternalServerError { t.Fatalf("revoke status=%d body=%s", w.Code, w.Body.String()) }

	w = request(http.MethodGet, "/api/v1/settings/general", nil, func(w *httptest.ResponseRecorder, r *http.Request) { h.generalSettings(w, r, actor) })
	if w.Code != http.StatusInternalServerError { t.Fatalf("settings get status=%d body=%s", w.Code, w.Body.String()) }

	w = request(http.MethodPut, "/api/v1/settings/general", map[string]any{"allowed_origins": "https://other.example.test"}, func(w *httptest.ResponseRecorder, r *http.Request) { h.generalSettings(w, r, actor) })
	if w.Code != http.StatusBadRequest { t.Fatalf("settings put status=%d body=%s", w.Code, w.Body.String()) }

	w = request(http.MethodGet, "/api/v1/admin/summary", nil, func(w *httptest.ResponseRecorder, r *http.Request) { h.summary(w, r) })
	if w.Code != http.StatusInternalServerError { t.Fatalf("summary status=%d body=%s", w.Code, w.Body.String()) }

	w = request(http.MethodGet, "/api/v1/system", nil, func(w *httptest.ResponseRecorder, r *http.Request) { h.system(w, r) })
	if w.Code != http.StatusInternalServerError { t.Fatalf("system status=%d body=%s", w.Code, w.Body.String()) }

	w = request(http.MethodPatch, "/api/v1/users/1", map[string]any{"enabled": false}, func(w *httptest.ResponseRecorder, r *http.Request) { h.userRoute(w, r, actor, auth.Session{}, "1") })
	if w.Code != http.StatusBadRequest { t.Fatalf("user mutation status=%d body=%s", w.Code, w.Body.String()) }
}
