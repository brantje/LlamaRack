package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestPhase10OIDCFrontendURLSetting(t *testing.T) {
	f := newAPIOIDCFixture(t)

	w := phase10Request(t, f.secured, http.MethodGet, "/api/v1/admin/auth/settings", nil, nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"frontend_url":{"value":"","source":"default","editable":true}`) {
		t.Fatalf("frontend URL default status=%d body=%s", w.Code, w.Body.String())
	}

	w = phase10Request(t, f.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"frontend_url": "http://192.168.60.5:3000"}, nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"frontend_url":{"value":"http://192.168.60.5:3000","source":"database","editable":true}`) {
		t.Fatalf("frontend URL update status=%d body=%s", w.Code, w.Body.String())
	}

	w = phase10Request(t, f.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"frontend_url": "javascript:alert(1)"}, nil, f.headers())
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "frontend URL must be an absolute HTTP(S) URL") {
		t.Fatalf("invalid frontend URL status=%d body=%s", w.Code, w.Body.String())
	}

	w = phase10Request(t, f.secured, http.MethodPut, "/api/v1/admin/auth/settings", map[string]any{"frontend_url": ""}, nil, f.headers())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"value":""`) {
		t.Fatalf("clear frontend URL status=%d body=%s", w.Code, w.Body.String())
	}
}
