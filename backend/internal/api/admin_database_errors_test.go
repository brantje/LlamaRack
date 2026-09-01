package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

func TestAdminHandlerDatabaseFailures(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.New(db, time.Hour)
	managerSettings := settings.New(db, settings.Defaults{
		SessionLifetime:   time.Hour,
		AllowedOrigins:    "http://manager.test",
		StartupTimeout:    time.Minute,
		AlwaysOnReconcile: time.Second,
	})
	h := &adminHandler{
		auth:     authService,
		settings: managerSettings,
		network:  managersecurity.NewNetwork(managerSettings),
		profile:  func() (llamacpp.Profile, error) { return llamacpp.Profile{}, nil },
		started:  time.Now(),
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	request := func(method, path string) *http.Request {
		return httptest.NewRequest(method, "http://manager.test"+path, nil)
	}
	assertInternal := func(name string, call func(http.ResponseWriter)) {
		t.Helper()
		w := httptest.NewRecorder()
		call(w)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s status=%d body=%s", name, w.Code, w.Body.String())
		}
	}

	assertInternal("list sessions", func(w http.ResponseWriter) {
		h.listSessions(w, request(http.MethodGet, "/api/v1/me/sessions"), 1, "")
	})
	assertInternal("list users", func(w http.ResponseWriter) {
		h.users(w, request(http.MethodGet, "/api/v1/users"), auth.User{ID: 1})
	})
	assertInternal("session revoke", func(w http.ResponseWriter) {
		h.sessionRoute(w, request(http.MethodDelete, "/api/v1/sessions/dead"), auth.User{ID: 1}, "dead")
	})
	assertInternal("general settings", func(w http.ResponseWriter) {
		h.generalSettings(w, request(http.MethodGet, "/api/v1/settings/general"), auth.User{ID: 1})
	})

	w := httptest.NewRecorder()
	h.changePassword(w, httptest.NewRequest(http.MethodPost, "http://manager.test/api/v1/me/password", strings.NewReader(`{"current_password":"old-password","new_password":"replacement-password","new_password_confirmation":"replacement-password"}`)), auth.User{ID: 1}, auth.Session{ID: "current"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("change password status=%d body=%s", w.Code, w.Body.String())
	}
}
