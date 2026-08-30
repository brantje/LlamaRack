package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagementPlaygroundProxyReentersGatewayAndKeepsLogs(t *testing.T) {
	f := newGatewayFixture(t, true)
	handler := NewManagementPlaygroundProxy(f.gateway)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/chat/completions", strings.NewReader(`{"model":"gateway-model","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Authorization", "Bearer management-token-must-not-reach-gateway")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"proxied":true`) || !strings.Contains(w.Body.String(), `"path":"/v1/chat/completions"`) {
		t.Fatalf("management bridge=%d %s", w.Code, w.Body.String())
	}
	requestID := w.Header().Get(headerRequestID)
	if requestID == "" {
		t.Fatal("management bridge did not preserve gateway request correlation")
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Endpoint != "/v1/chat/completions" || record.InstanceID != "gateway-model" || record.Result != "success" {
		t.Fatalf("management bridge observability=%+v", record)
	}
	if record.APIKey == nil || record.APIKey.Name != "Management Playground" || record.APIKey.ID != "" || record.APIKey.Prefix != "" {
		t.Fatalf("management bridge should log its source without creating an API key: %+v", record.APIKey)
	}
}

func TestManagementPlaygroundProxyRejectsNonPost(t *testing.T) {
	called := false
	handler := NewManagementPlaygroundProxy(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/playground/chat/completions", nil))
	if w.Code != http.StatusMethodNotAllowed || called {
		t.Fatalf("non-POST bridge request status=%d called=%v", w.Code, called)
	}
}
