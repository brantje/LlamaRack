package api

import (
	"net/http"
	"testing"
)

func TestPhase10OIDCAdditionalErrorEdges(t *testing.T) {
	f := newAPIOIDCFixture(t)
	headers := f.headers()

	cases := []struct {
		name    string
		handler http.Handler
		method  string
		path    string
		body    any
		headers map[string]string
		status  int
	}{
		{"ws ticket without auth context", f.raw, http.MethodPost, "/api/v1/auth/ws-ticket", nil, nil, http.StatusUnauthorized},
		{"exchange malformed shape", f.raw, http.MethodPost, "/api/v1/auth/oidc/exchange", []string{"invalid"}, nil, http.StatusBadRequest},
		{"exchange missing code", f.raw, http.MethodPost, "/api/v1/auth/oidc/exchange", map[string]string{}, nil, http.StatusUnauthorized},
		{"malformed oidc flow", f.raw, http.MethodGet, "/api/v1/auth/oidc/provider-only", nil, nil, http.StatusNotFound},
		{"oidc start wrong method", f.raw, http.MethodPost, "/api/v1/auth/oidc/missing/start", nil, nil, http.StatusMethodNotAllowed},
		{"oidc callback wrong method", f.raw, http.MethodPost, "/api/v1/auth/oidc/missing/callback", nil, nil, http.StatusMethodNotAllowed},
		{"oidc start missing provider", f.raw, http.MethodGet, "/api/v1/auth/oidc/missing/start", nil, nil, http.StatusBadRequest},
		{"oidc callback missing state", f.raw, http.MethodGet, "/api/v1/auth/oidc/missing/callback", nil, nil, http.StatusUnauthorized},
		{"settings wrong method", f.secured, http.MethodPost, "/api/v1/admin/auth/settings", nil, headers, http.StatusMethodNotAllowed},
		{"settings malformed shape", f.secured, http.MethodPut, "/api/v1/admin/auth/settings", []string{"invalid"}, headers, http.StatusBadRequest},
		{"providers wrong method", f.secured, http.MethodPut, "/api/v1/admin/auth/providers", nil, headers, http.StatusMethodNotAllowed},
		{"providers malformed create", f.secured, http.MethodPost, "/api/v1/admin/auth/providers", []string{"invalid"}, headers, http.StatusBadRequest},
		{"provider malformed path", f.secured, http.MethodPost, "/api/v1/admin/auth/providers/a/b/c", nil, headers, http.StatusNotFound},
		{"provider test wrong method", f.secured, http.MethodGet, "/api/v1/admin/auth/providers/missing/test", nil, headers, http.StatusNotFound},
		{"provider missing get", f.secured, http.MethodGet, "/api/v1/admin/auth/providers/missing", nil, headers, http.StatusNotFound},
		{"provider missing update", f.secured, http.MethodPut, "/api/v1/admin/auth/providers/missing", f.providerInput("Missing"), headers, http.StatusNotFound},
		{"provider missing delete", f.secured, http.MethodDelete, "/api/v1/admin/auth/providers/missing", nil, headers, http.StatusNotFound},
		{"identities wrong method", f.secured, http.MethodPut, "/api/v1/admin/auth/identities", nil, headers, http.StatusMethodNotAllowed},
		{"identities malformed create", f.secured, http.MethodPost, "/api/v1/admin/auth/identities", []string{"invalid"}, headers, http.StatusBadRequest},
		{"identities invalid provider", f.secured, http.MethodPost, "/api/v1/admin/auth/identities", map[string]any{"user_id": 1, "provider_id": "missing", "subject": "subject"}, headers, http.StatusBadRequest},
		{"identity wrong method", f.secured, http.MethodGet, "/api/v1/admin/auth/identities/missing", nil, headers, http.StatusMethodNotAllowed},
		{"identity malformed path", f.secured, http.MethodDelete, "/api/v1/admin/auth/identities/a/b", nil, headers, http.StatusMethodNotAllowed},
		{"identity missing delete", f.secured, http.MethodDelete, "/api/v1/admin/auth/identities/missing", nil, headers, http.StatusNotFound},
		{"my identities without context", f.raw, http.MethodGet, "/api/v1/me/identities", nil, nil, http.StatusUnauthorized},
		{"unknown route", f.raw, http.MethodGet, "/api/v1/admin/auth/unknown", nil, nil, http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := phase10Request(t, tc.handler, tc.method, tc.path, tc.body, nil, tc.headers)
			if w.Code != tc.status {
				t.Fatalf("%s %s status=%d want=%d body=%s", tc.method, tc.path, w.Code, tc.status, w.Body.String())
			}
		})
	}
}
