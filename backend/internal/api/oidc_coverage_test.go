package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/auth"
)

func TestOIDCManagementRouteErrorAndMutationBranches(t *testing.T) {
	f := newAPIOIDCFixture(t)

	w := adminRequest(t, f.raw, http.MethodGet, "/api/v1/me/identities", nil, nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("raw me identities status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.raw, http.MethodPost, "/api/v1/auth/ws-ticket", nil, nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("raw ws ticket status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.raw, http.MethodPost, "/api/v1/auth/oidc/exchange", []string{"invalid"}, nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid exchange body status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers", []string{"invalid"}, nil, f.headers())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid provider body status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers", map[string]any{"name": "missing-fields"}, nil, f.headers())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid provider input status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodPut, "/api/v1/admin/auth/settings", []string{"invalid"}, nil, f.headers())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid auth settings body status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodPost, "/api/v1/admin/auth/settings", nil, nil, f.headers())
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("auth settings method status=%d", w.Code)
	}
	w = adminRequest(t, f.secured, http.MethodPut, "/api/v1/admin/auth/providers", nil, nil, f.headers())
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("providers method status=%d", w.Code)
	}

	w = adminRequest(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers", f.providerInput("Coverage"), nil, f.headers())
	if w.Code != http.StatusCreated {
		t.Fatalf("create provider status=%d body=%s", w.Code, w.Body.String())
	}
	var provider auth.OIDCProvider
	decodeAPIJSON(t, w, &provider)

	changed := f.providerInput("Coverage changed")
	delete(changed, "client_secret")
	w = adminRequest(t, f.secured, http.MethodPut, "/api/v1/admin/auth/providers/"+provider.ID, changed, nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Coverage changed") {
		t.Fatalf("provider put status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodPatch, "/api/v1/admin/auth/providers/"+provider.ID, changed, nil, f.headers())
	if w.Code != http.StatusOK {
		t.Fatalf("provider patch status=%d body=%s", w.Code, w.Body.String())
	}

	w = adminRequest(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers/missing/test", nil, nil, f.headers())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing provider test status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodDelete, "/api/v1/admin/auth/providers/missing", nil, nil, f.headers())
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing provider delete status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers/"+provider.ID+"/unknown", nil, nil, f.headers())
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown provider subroute status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodGet, "/api/v1/admin/auth/providers/"+provider.ID+"/too/many", nil, nil, f.headers())
	if w.Code != http.StatusNotFound {
		t.Fatalf("provider too many segments status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers/"+provider.ID, nil, nil, f.headers())
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("provider item method status=%d body=%s", w.Code, w.Body.String())
	}

	w = adminRequest(t, f.secured, http.MethodPost, "/api/v1/admin/auth/identities", []string{"invalid"}, nil, f.headers())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid identity body status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodPost, "/api/v1/admin/auth/identities", map[string]any{"user_id": 9999, "provider_id": provider.ID, "subject": "subject"}, nil, f.headers())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid identity link status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodPut, "/api/v1/admin/auth/identities", nil, nil, f.headers())
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("identities method status=%d", w.Code)
	}
	w = adminRequest(t, f.secured, http.MethodDelete, "/api/v1/admin/auth/identities/missing", nil, nil, f.headers())
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing identity delete status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodGet, "/api/v1/admin/auth/identities/missing", nil, nil, f.headers())
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("identity get status=%d body=%s", w.Code, w.Body.String())
	}
	w = adminRequest(t, f.secured, http.MethodDelete, "/api/v1/admin/auth/identities/a/b", nil, nil, f.headers())
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("identity nested delete status=%d body=%s", w.Code, w.Body.String())
	}
}
