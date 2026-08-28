package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/database"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

type phase10SecurityFixture struct {
	handler   http.Handler
	auth      *auth.Service
	settings  *settings.Service
	network   *managersecurity.Network
	protector *managersecurity.LoginProtector
}

func newPhase10SecurityFixture(t *testing.T) *phase10SecurityFixture {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "phase10-security.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = db.Close() })
	managerSettings := settings.New(db, settings.Defaults{SessionLifetime: time.Hour, AllowedOrigins: "http://localhost:3000", StartupTimeout: time.Minute, AlwaysOnReconcile: time.Second})
	authService := auth.New(db, time.Hour)
	network := managersecurity.NewNetwork(managerSettings)
	protector := managersecurity.NewLoginProtector(managerSettings)
	authHandler := NewPhase10AuthHandler(authService, network, protector)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/auth/", authHandler)
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/api/v1/protected", func(w http.ResponseWriter, r *http.Request) {
		user, session, ok := managementAuthFromRequest(r)
		if !ok { writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "missing management auth context"}); return }
		writeJSON(w, http.StatusOK, map[string]any{"user_id": user.ID, "session_id": session.ID})
	})
	return &phase10SecurityFixture{handler: ManagementSecurity(authService, network, mux), auth: authService, settings: managerSettings, network: network, protector: protector}
}

func phase10Request(t *testing.T, h http.Handler, method, path string, body any, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil { t.Fatal(err) }
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.RemoteAddr = "192.0.2.10:4321"
	r.Header.Set("User-Agent", "phase10-security-test")
	for _, cookie := range cookies { if cookie != nil { r.AddCookie(cookie) } }
	for key, value := range headers { r.Header.Set(key, value) }
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPhase10BootstrapLoginBearerAndLogout(t *testing.T) {
	f := newPhase10SecurityFixture(t)
	w := phase10Request(t, f.handler, http.MethodGet, "/api/v1/auth/bootstrap", nil, nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"required":true`) { t.Fatalf("bootstrap status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.handler, http.MethodPost, "/api/v1/auth/bootstrap", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil, nil)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"username":"admin"`) { t.Fatalf("bootstrap create status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.handler, http.MethodGet, "/api/v1/auth/bootstrap", nil, nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"required":false`) { t.Fatalf("bootstrap complete status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil, map[string]string{"Origin": "https://evil.example"})
	if w.Code != http.StatusForbidden { t.Fatalf("foreign-origin login status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil, nil)
	if w.Code != http.StatusOK { t.Fatalf("login status=%d body=%s", w.Code, w.Body.String()) }
	var result auth.LoginResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil { t.Fatal(err) }
	if result.AccessToken == "" || result.TokenType != "Bearer" || result.User.Username != "admin" || result.ExpiresAt <= time.Now().Unix() { t.Fatalf("unexpected login result: %+v", result) }
	if len(w.Result().Cookies()) != 0 { t.Fatalf("management login must not set auth cookies: %+v", w.Result().Cookies()) }
	headers := map[string]string{"Authorization": "Bearer " + result.AccessToken}
	w = phase10Request(t, f.handler, http.MethodGet, "/api/v1/protected", nil, nil, headers)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"user_id":1`) { t.Fatalf("protected get status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.handler, http.MethodPost, "/api/v1/protected", nil, nil, headers)
	if w.Code != http.StatusOK { t.Fatalf("bearer mutation status=%d body=%s", w.Code, w.Body.String()) }
	legacyCookie := &http.Cookie{Name: sessionCookie, Value: result.AccessToken}
	w = phase10Request(t, f.handler, http.MethodGet, "/api/v1/protected", nil, []*http.Cookie{legacyCookie}, nil)
	if w.Code != http.StatusUnauthorized { t.Fatalf("cookie-only auth status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.handler, http.MethodGet, "/api/v1/protected", nil, nil, map[string]string{"Authorization": "Basic nope"})
	if w.Code != http.StatusUnauthorized { t.Fatalf("malformed bearer status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.handler, http.MethodPost, "/api/v1/auth/logout", nil, nil, headers)
	if w.Code != http.StatusNoContent { t.Fatalf("logout status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.handler, http.MethodGet, "/api/v1/protected", nil, nil, headers)
	if w.Code != http.StatusUnauthorized { t.Fatalf("revoked session status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, f.handler, http.MethodGet, "/api/v1/health", nil, nil, nil)
	if w.Code != http.StatusNoContent { t.Fatalf("health status=%d", w.Code) }
}

func TestPhase10LoginFailuresRateLimitAndRouteFallbacks(t *testing.T) {
	f := newPhase10SecurityFixture(t)
	if _, err := f.auth.Bootstrap(t.Context(), "admin", "correct-horse-battery"); err != nil { t.Fatal(err) }
	w := phase10Request(t, f.handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong-password"}, nil, nil)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "invalid username or password") { t.Fatalf("invalid login status=%d body=%s", w.Code, w.Body.String()) }
	if _, err := f.settings.Set(t.Context(), settings.LoginFailureThreshold, 2); err != nil { t.Fatal(err) }
	f.protector.Success("admin", "192.0.2.10")
	if f.protector.Failure(t.Context(), "admin", "192.0.2.10") { t.Fatal("first seeded failure unexpectedly locked") }
	if !f.protector.Failure(t.Context(), "admin", "192.0.2.10") { t.Fatal("second seeded failure should lock") }
	w = phase10Request(t, f.handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil, nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" { t.Fatalf("rate limited status=%d retry=%q body=%s", w.Code, w.Header().Get("Retry-After"), w.Body.String()) }
	w = phase10Request(t, f.handler, http.MethodGet, "/api/v1/protected", nil, nil, nil)
	if w.Code != http.StatusUnauthorized { t.Fatalf("missing bearer status=%d body=%s", w.Code, w.Body.String()) }
	w = phase10Request(t, NewPhase10AuthHandler(f.auth, f.network, f.protector), http.MethodGet, "/api/v1/auth/nope", nil, nil, nil)
	if w.Code != http.StatusNotFound { t.Fatalf("auth route fallback status=%d body=%s", w.Code, w.Body.String()) }
	for _, method := range []string{http.MethodGet, http.MethodOptions, http.MethodHead} { if isStateChanging(method) { t.Fatalf("%s should not be state-changing", method) } }
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} { if !isStateChanging(method) { t.Fatalf("%s should be state-changing", method) } }
}
