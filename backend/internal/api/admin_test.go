package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	managersecurity "github.com/brantje/llamarack/backend/internal/security"
	"github.com/brantje/llamarack/backend/internal/settings"
)

type adminFixture struct {
	handler  http.Handler
	auth     *auth.Service
	settings *settings.Service
	cookie   *http.Cookie
}

func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	token, _, _, err := authService.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "192.0.2.10", "Chrome/100 Windows")
	if err != nil {
		t.Fatal(err)
	}
	managerSettings := settings.New(db, settings.Defaults{
		SessionLifetime:   time.Hour,
		AllowedOrigins:    "http://manager.test",
		StartupTimeout:    3 * time.Minute,
		AlwaysOnReconcile: 15 * time.Second,
		DataDir:           root,
		ModelsDir:         filepath.Join(root, "models"),
		DatabasePath:      filepath.Join(root, "manager.db"),
		ListenAddr:        ":8000",
		LlamaServerPath:   "/app/llama-server",
	})
	secrets, err := huggingface.NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	network := managersecurity.NewNetwork(managerSettings)
	profile := func() (llamacpp.Profile, error) {
		return llamacpp.Profile{Path: "/app/llama-server", Version: "test", Fingerprint: "abc", Options: []llamacpp.Option{{Key: "ctx-size"}}}, nil
	}
	return &adminFixture{
		handler:  NewAdminHandler(authService, managerSettings, secrets, network, profile),
		auth:     authService,
		settings: managerSettings,
		cookie:   &http.Cookie{Name: sessionCookie, Value: token},
	}
}

func TestAdminRequiresAuthenticationAndExposesProfile(t *testing.T) {
	f := newAdminFixture(t)
	w := doRequest(t, f.handler, http.MethodGet, "/api/v1/me", nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized me=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodGet, "/api/v1/me", nil, f.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"username":"admin"`) || strings.Contains(w.Body.String(), "password") {
		t.Fatalf("me=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodGet, "/api/v1/nope", nil, f.cookie)
	if w.Code != http.StatusNotFound {
		t.Fatalf("not found=%d", w.Code)
	}
}

func TestAdminUserManagementSafeguardsAndSessionCounts(t *testing.T) {
	f := newAdminFixture(t)

	w := doRequest(t, f.handler, http.MethodPost, "/api/v1/users", map[string]string{"username": "operator", "password": "operator-password"}, f.cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user=%d body=%s", w.Code, w.Body.String())
	}
	var operator auth.User
	if err := json.Unmarshal(w.Body.Bytes(), &operator); err != nil {
		t.Fatal(err)
	}
	if operator.ID == 0 {
		t.Fatal("missing operator id")
	}
	operatorToken, _, _, err := f.auth.LoginWithMetadata(t.Context(), "operator", "operator-password", "198.51.100.3", "Firefox/100 Linux")
	if err != nil {
		t.Fatal(err)
	}

	w = doRequest(t, f.handler, http.MethodGet, "/api/v1/users", nil, f.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"active_sessions":1`) || !strings.Contains(w.Body.String(), `"created_at":`) {
		t.Fatalf("list users=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodGet, "/api/v1/users/"+itoa(operator.ID)+"/sessions", nil, f.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Firefox/100 Linux") || !strings.Contains(w.Body.String(), "198.51.100.3") {
		t.Fatalf("operator sessions=%d body=%s", w.Code, w.Body.String())
	}

	w = doRequest(t, f.handler, http.MethodPatch, "/api/v1/users/"+itoa(operator.ID), map[string]bool{"enabled": false}, f.cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("disable user=%d body=%s", w.Code, w.Body.String())
	}
	if _, _, err := f.auth.SessionUserWithSession(t.Context(), operatorToken); err == nil {
		t.Fatal("disabling user must revoke sessions")
	}
	w = doRequest(t, f.handler, http.MethodPatch, "/api/v1/users/"+itoa(operator.ID), map[string]bool{"enabled": true}, f.cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("enable user=%d body=%s", w.Code, w.Body.String())
	}

	operatorToken, _, _, err = f.auth.LoginWithMetadata(t.Context(), "operator", "operator-password", "", "")
	if err != nil {
		t.Fatal(err)
	}
	w = doRequest(t, f.handler, http.MethodPost, "/api/v1/users/"+itoa(operator.ID)+"/password", map[string]string{"password": "replacement-password"}, f.cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("reset password=%d body=%s", w.Code, w.Body.String())
	}
	if _, _, err := f.auth.SessionUserWithSession(t.Context(), operatorToken); err == nil {
		t.Fatal("password reset must revoke sessions")
	}

	w = doRequest(t, f.handler, http.MethodDelete, "/api/v1/users/1", nil, f.cookie)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "current management user") {
		t.Fatalf("self delete=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodDelete, "/api/v1/users/"+itoa(operator.ID), nil, f.cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete operator=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodPatch, "/api/v1/users/1", map[string]bool{"enabled": false}, f.cookie)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "last enabled") {
		t.Fatalf("last enabled disable=%d body=%s", w.Code, w.Body.String())
	}

	w = doRequest(t, f.handler, http.MethodPatch, "/api/v1/users/1", map[string]any{}, f.cookie)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled=%d", w.Code)
	}
	w = doRequest(t, f.handler, http.MethodGet, "/api/v1/users/not-a-number", nil, f.cookie)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bad user id=%d", w.Code)
	}
}

func TestAdminSelfServicePasswordAndSessions(t *testing.T) {
	f := newAdminFixture(t)
	secondToken, _, _, err := f.auth.LoginWithMetadata(t.Context(), "admin", "correct-horse-battery", "203.0.113.4", "Safari/17 Mac OS X")
	if err != nil {
		t.Fatal(err)
	}

	w := doRequest(t, f.handler, http.MethodGet, "/api/v1/me/sessions", nil, f.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"current":true`) || !strings.Contains(w.Body.String(), "Safari/17 Mac OS X") {
		t.Fatalf("me sessions=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodPost, "/api/v1/me/password", map[string]string{
		"current_password": "correct-horse-battery", "new_password": "new-password-one", "new_password_confirmation": "different-password",
	}, f.cookie)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "confirmation") {
		t.Fatalf("password mismatch=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodPost, "/api/v1/me/password", map[string]string{
		"current_password": "wrong-password", "new_password": "new-password-one", "new_password_confirmation": "new-password-one",
	}, f.cookie)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "current password is invalid") {
		t.Fatalf("invalid current password=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodPost, "/api/v1/me/password", map[string]string{
		"current_password": "correct-horse-battery", "new_password": "new-password-one", "new_password_confirmation": "new-password-one",
	}, f.cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("password change=%d body=%s", w.Code, w.Body.String())
	}
	if _, _, err := f.auth.SessionUserWithSession(t.Context(), secondToken); err == nil {
		t.Fatal("password change must revoke other sessions")
	}
	if _, _, err := f.auth.SessionUserWithSession(t.Context(), f.cookie.Value); err != nil {
		t.Fatalf("password change must preserve current session: %v", err)
	}

	thirdToken, _, _, err := f.auth.LoginWithMetadata(t.Context(), "admin", "new-password-one", "", "third")
	if err != nil {
		t.Fatal(err)
	}
	w = doRequest(t, f.handler, http.MethodPost, "/api/v1/me/sessions/revoke-others", nil, f.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"revoked":1`) {
		t.Fatalf("revoke others=%d body=%s", w.Code, w.Body.String())
	}
	if _, _, err := f.auth.SessionUserWithSession(t.Context(), thirdToken); err == nil {
		t.Fatal("other session should be revoked")
	}

	fourthToken, _, _, err := f.auth.LoginWithMetadata(t.Context(), "admin", "new-password-one", "", "fourth")
	if err != nil {
		t.Fatal(err)
	}
	_, fourthSession, err := f.auth.SessionUserWithSession(t.Context(), fourthToken)
	if err != nil {
		t.Fatal(err)
	}
	w = doRequest(t, f.handler, http.MethodDelete, "/api/v1/sessions/"+fourthSession.ID, nil, f.cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke session=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodDelete, "/api/v1/sessions/missing", nil, f.cookie)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing session=%d", w.Code)
	}

	fifthToken, _, _, err := f.auth.LoginWithMetadata(t.Context(), "admin", "new-password-one", "", "fifth")
	if err != nil {
		t.Fatal(err)
	}
	_, fifthSession, err := f.auth.SessionUserWithSession(t.Context(), fifthToken)
	if err != nil {
		t.Fatal(err)
	}
	w = doRequest(t, f.handler, http.MethodDelete, "/api/v1/me/sessions/"+fifthSession.ID, nil, f.cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke own session=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodDelete, "/api/v1/me/sessions/missing", nil, f.cookie)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing own session=%d", w.Code)
	}
	w = doRequest(t, f.handler, http.MethodPost, "/api/v1/me/sessions/revoke-all", nil, f.cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke all=%d body=%s", w.Code, w.Body.String())
	}
	if _, _, err := f.auth.SessionUserWithSession(t.Context(), f.cookie.Value); err == nil {
		t.Fatal("revoke all must include current session")
	}
}

func TestAdminGeneralSettingsSummaryAndSystem(t *testing.T) {
	f := newAdminFixture(t)
	w := doRequest(t, f.handler, http.MethodGet, "/api/v1/settings/general", nil, f.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"source":"default"`) || !strings.Contains(w.Body.String(), `"database_path"`) {
		t.Fatalf("settings get=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodPut, "/api/v1/settings/general", map[string]any{
		"session_lifetime_seconds":          7200,
		"login_failure_threshold":           6,
		"trusted_proxies":                   "10.0.0.0/8",
		"allowed_origins":                   "https://manager.example.test",
		"external_url":                      "https://manager.example.test",
		"startup_timeout_seconds":           240,
		"idle_unload_seconds":               600,
		"always_on_reconcile_seconds":       20,
		"max_pending_requests_per_instance": 16,
		"max_pending_requests_global":       64,
	}, f.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"source":"database"`) {
		t.Fatalf("settings put=%d body=%s", w.Code, w.Body.String())
	}
	if f.auth.SessionLifetime() != 2*time.Hour {
		t.Fatalf("session lifetime=%v", f.auth.SessionLifetime())
	}
	w = doRequest(t, f.handler, http.MethodPut, "/api/v1/settings/general", map[string]any{"session_lifetime_seconds": 1}, f.cookie)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid setting=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodPost, "/api/v1/settings/general", nil, f.cookie)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("settings method=%d", w.Code)
	}

	w = doRequest(t, f.handler, http.MethodGet, "/api/v1/admin/summary", nil, f.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"enabled":1`) || !strings.Contains(w.Body.String(), `"available":true`) || !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatalf("summary=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.handler, http.MethodGet, "/api/v1/system", nil, f.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"uptime_seconds"`) || !strings.Contains(w.Body.String(), `"secure_cookie":true`) || !strings.Contains(w.Body.String(), `"options":1`) {
		t.Fatalf("system=%d body=%s", w.Code, w.Body.String())
	}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
