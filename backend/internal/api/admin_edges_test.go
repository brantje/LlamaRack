package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
)

func TestAdminManagementRouteEdges(t *testing.T) {
	f := newAdminFixture(t)

	cases := []struct {
		method string
		path   string
		body   any
		status int
	}{
		{http.MethodPost, "/api/v1/users", map[string]string{"username": "x", "password": "short"}, http.StatusBadRequest},
		{http.MethodPut, "/api/v1/users", nil, http.StatusMethodNotAllowed},
		{http.MethodPatch, "/api/v1/users/999999", map[string]bool{"enabled": false}, http.StatusNotFound},
		{http.MethodGet, "/api/v1/users/1/too/many", nil, http.StatusNotFound},
		{http.MethodGet, "/api/v1/users/1/password", nil, http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v1/users/1/password", map[string]string{"password": "short"}, http.StatusBadRequest},
		{http.MethodPost, "/api/v1/users/1/sessions", nil, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/users/1/unknown", nil, http.StatusNotFound},
		{http.MethodPost, "/api/v1/sessions/not-delete", nil, http.StatusMethodNotAllowed},
		{http.MethodDelete, "/api/v1/sessions/a/b", nil, http.StatusMethodNotAllowed},
		{http.MethodPut, "/api/v1/settings/general", []byte("{"), http.StatusBadRequest},
	}
	for _, tc := range cases {
		w := doRequest(t, f.handler, tc.method, tc.path, tc.body, f.cookie)
		if w.Code != tc.status {
			t.Fatalf("%s %s status=%d want=%d body=%s", tc.method, tc.path, w.Code, tc.status, w.Body.String())
		}
	}

	w := doRequest(t, f.handler, http.MethodPut, "/api/v1/settings/general", map[string]any{
		"login_protection_enabled": false,
		"login_lockout_seconds": 120,
	}, f.cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"login_protection_enabled"`) || !strings.Contains(w.Body.String(), `"login_lockout_seconds"`) {
		t.Fatalf("additional settings status=%d body=%s", w.Code, w.Body.String())
	}

	h := f.handler.(*adminHandler)
	h.profile = func() (llamacpp.Profile, error) { return llamacpp.Profile{}, errors.New("llama unavailable") }
	for _, path := range []string{"/api/v1/admin/summary", "/api/v1/system"} {
		w = doRequest(t, f.handler, http.MethodGet, path, nil, f.cookie)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"available":false`) {
			t.Fatalf("unavailable profile %s status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
}

func TestAPIKeyRouteEdges(t *testing.T) {
	f := newAPIFixture(t, nil)
	handler := apiKeyHandler(t, f)

	cases := []struct {
		method string
		path   string
		body   any
		status int
	}{
		{http.MethodPut, "/api/v1/api-keys", nil, http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v1/api-keys", []byte("{"), http.StatusBadRequest},
		{http.MethodGet, "/api/v1/api-keys/missing", nil, http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v1/api-keys/missing/unknown", nil, http.StatusNotFound},
		{http.MethodPost, "/api/v1/api-keys/missing/revoke", nil, http.StatusNotFound},
		{http.MethodPost, "/api/v1/api-keys/missing/rotate", nil, http.StatusNotFound},
		{http.MethodPatch, "/api/v1/api-keys/missing", []byte("{"), http.StatusBadRequest},
	}
	for _, tc := range cases {
		w := doRequest(t, handler, tc.method, tc.path, tc.body, nil)
		if w.Code != tc.status {
			t.Fatalf("%s %s status=%d want=%d body=%s", tc.method, tc.path, w.Code, tc.status, w.Body.String())
		}
	}
}

func TestAdminAuthHandlerRejectedInputsAndSecureCookies(t *testing.T) {
	f := newAuthSecurityFixture(t)
	authHandler := NewAuthHandler(f.auth, f.network, f.protector, f.settings)

	w := adminRequest(t, authHandler, http.MethodPost, "/api/v1/auth/bootstrap", []string{"invalid"}, nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid bootstrap status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := f.auth.Bootstrap(t.Context(), "admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	w = adminRequest(t, authHandler, http.MethodPost, "/api/v1/auth/bootstrap", map[string]string{"username": "other", "password": "correct-horse-battery"}, nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate bootstrap status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, authHandler, http.MethodPost, "/api/v1/auth/login", []string{"invalid"}, nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid login body status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, authHandler, http.MethodGet, "/api/v1/auth/logout", nil, nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("wrong logout method status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, authHandler, http.MethodPost, "/api/v1/auth/logout", nil, nil, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("cookie-less logout status=%d body=%s", w.Code, w.Body.String())
	}

	recorder := httptest.NewRecorder()
	SetSessionCookies(recorder, "session", "csrf", 0, true)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 || !cookies[0].Secure || !cookies[1].Secure || cookies[0].MaxAge != 1 || cookies[1].MaxAge != 1 {
		t.Fatalf("secure cookie policy=%+v", cookies)
	}
}
