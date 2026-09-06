package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/observability"
)

func TestManagementPlaygroundProxyReentersGatewayAndKeepsLogs(t *testing.T) {
	f := newGatewayFixture(t, true)
	handler := NewManagementPlaygroundProxy(f.gateway)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/chat/completions", strings.NewReader(`{"model":"gateway-model","messages":[{"role":"user","content":"hello"}]}`))
	req = req.WithContext(auth.WithTrustedInferenceContext(req.Context(), auth.TrustedInferencePrincipal{
		Kind: observability.OwnerKindManagementUser,
		ID:   fmt.Sprintf("%d", f.ownerID),
	}))
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
	if record.Endpoint != "/v1/chat/completions" || record.InstanceID != f.instanceID || record.Result != "success" {
		t.Fatalf("management bridge observability=%+v", record)
	}
	if record.APIKey == nil || record.APIKey.Name != "Management Playground" || record.APIKey.ID != "" || record.APIKey.Prefix != "" {
		t.Fatalf("management bridge should log its source without creating an API key: %+v", record.APIKey)
	}
}

func TestManagementPlaygroundProxyReplacesManagementBearerBeforeGateway(t *testing.T) {
	var seenAuthorization, seenPath string
	handler := NewManagementPlaygroundProxy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuthorization = r.Header.Get("Authorization")
		seenPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer real-management-secret")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent || seenPath != "/v1/chat/completions" {
		t.Fatalf("bridge rewrite status=%d path=%q", w.Code, seenPath)
	}
	if seenAuthorization != managementPlaygroundBearer || strings.Contains(seenAuthorization, "real-management-secret") {
		t.Fatalf("management bearer was not safely replaced: %q", seenAuthorization)
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
