package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestOIDCOutboundTrustSettings(t *testing.T) {
	f := newAPIOIDCFixture(t)

	w := adminRequest(t, f.secured, http.MethodGet, "/api/v1/admin/auth/settings", nil, nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"oidc_allow_http"`) || !strings.Contains(w.Body.String(), `"oidc_allowed_hosts"`) {
		t.Fatalf("outbound settings missing status=%d body=%s", w.Code, w.Body.String())
	}

	w = adminRequest(t, f.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{
		"oidc_allow_http":    false,
		"oidc_allowed_hosts": "authentik.lan,192.168.1.10",
	}, nil, f.headers())
	if w.Code != http.StatusOK {
		t.Fatalf("outbound settings update status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `"oidc_allow_http":{"value":false,"source":"database","editable":true}`) {
		t.Fatalf("allow http update body=%s", body)
	}
	if !strings.Contains(body, `"oidc_allowed_hosts":{"value":"authentik.lan,192.168.1.10","source":"database","editable":true}`) {
		t.Fatalf("allowed hosts update body=%s", body)
	}
}
