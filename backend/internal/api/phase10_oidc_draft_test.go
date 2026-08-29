package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestPhase10OIDCDraftProviderTestRoute(t *testing.T) {
	f := newAPIOIDCFixture(t)

	w := phase10Request(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers/test", f.providerInput("Draft"), nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("draft test without bearer=%d body=%s", w.Code, w.Body.String())
	}

	w = phase10Request(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers/test", f.providerInput("Draft"), nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("draft test=%d body=%s", w.Code, w.Body.String())
	}

	w = phase10Request(t, f.secured, http.MethodGet, "/api/v1/admin/auth/providers", nil, nil, f.headers())
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("draft test persisted provider: status=%d body=%s", w.Code, w.Body.String())
	}

	missingSecret := f.providerInput("Draft")
	delete(missingSecret, "client_secret")
	w = phase10Request(t, f.secured, http.MethodPost, "/api/v1/admin/auth/providers/test", missingSecret, nil, f.headers())
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "client_secret is required") {
		t.Fatalf("draft test missing secret=%d body=%s", w.Code, w.Body.String())
	}

	w = phase10Request(t, f.raw, http.MethodGet, "/api/v1/admin/auth/providers/test", nil, nil, nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("draft test GET=%d body=%s", w.Code, w.Body.String())
	}
}
