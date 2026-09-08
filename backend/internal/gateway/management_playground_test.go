package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/observability"
)

func TestManagementPlaygroundProxyReentersGatewayAndKeepsLogs(t *testing.T) {
	f := newGatewayFixture(t, true)
	if _, err := f.lifecycle.Instances().Update(context.Background(), f.instanceID, instances.UpdateInput{
		Name: "Gateway model", RequestLogMode: "full",
	}); err != nil {
		t.Fatal(err)
	}
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
	if record.RequestBody == nil {
		t.Fatal("full request logging did not retain Playground request")
	}
	var forwarded map[string]any
	if err := json.Unmarshal([]byte(*record.RequestBody), &forwarded); err != nil {
		t.Fatal(err)
	}
	if enabled, ok := forwarded["timings_per_token"].(bool); !ok || !enabled {
		t.Fatalf("Playground request did not inject timings_per_token=true: %s", *record.RequestBody)
	}
}

func TestManagementPlaygroundProxyPreservesExplicitTimingsPreference(t *testing.T) {
	f := newGatewayFixture(t, true)
	if _, err := f.lifecycle.Instances().Update(context.Background(), f.instanceID, instances.UpdateInput{
		Name: "Gateway model", RequestLogMode: "full",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewManagementPlaygroundProxy(f.gateway)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/chat/completions", strings.NewReader(`{"model":"gateway-model","messages":[],"timings_per_token":false}`))
	req = req.WithContext(auth.WithTrustedInferenceContext(req.Context(), auth.TrustedInferencePrincipal{
		Kind: observability.OwnerKindManagementUser,
		ID:   fmt.Sprintf("%d", f.ownerID),
	}))
	req.Header.Set("Authorization", "Bearer management-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("management bridge=%d %s", w.Code, w.Body.String())
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
	if err != nil || record.RequestBody == nil {
		t.Fatalf("request log=%+v err=%v", record, err)
	}
	var forwarded map[string]any
	if err := json.Unmarshal([]byte(*record.RequestBody), &forwarded); err != nil {
		t.Fatal(err)
	}
	if value, ok := forwarded["timings_per_token"].(bool); !ok || value {
		t.Fatalf("explicit timings_per_token=false was not preserved: %s", *record.RequestBody)
	}
}

func TestManagementPlaygroundProxyReplacesManagementBearerBeforeGateway(t *testing.T) {
	var seenAuthorization, seenPath string
	var marked bool
	handler := NewManagementPlaygroundProxy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuthorization = r.Header.Get("Authorization")
		seenPath = r.URL.Path
		marked = isManagementPlaygroundRequest(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer real-management-secret")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent || seenPath != "/v1/chat/completions" || !marked {
		t.Fatalf("bridge rewrite status=%d path=%q marked=%v", w.Code, seenPath, marked)
	}
	if seenAuthorization != managementPlaygroundBearer || strings.Contains(seenAuthorization, "real-management-secret") {
		t.Fatalf("management bearer was not safely replaced: %q", seenAuthorization)
	}
}

func TestEnsurePlaygroundTimings(t *testing.T) {
	tests := []struct {
		name string
		body string
		want *bool
	}{
		{name: "missing", body: `{"model":"one"}`, want: boolPointer(true)},
		{name: "explicit false", body: `{"model":"one","timings_per_token":false}`, want: boolPointer(false)},
		{name: "explicit true", body: `{"model":"one","timings_per_token":true}`, want: boolPointer(true)},
		{name: "malformed", body: `{bad`, want: nil},
		{name: "array", body: `[]`, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ensurePlaygroundTimings([]byte(test.body))
			var object map[string]any
			if json.Unmarshal(got, &object) != nil || object == nil {
				if test.want != nil {
					t.Fatalf("expected JSON object, got %q", string(got))
				}
				return
			}
			value, ok := object["timings_per_token"].(bool)
			if test.want == nil {
				if ok {
					t.Fatalf("unexpected timings value %v", value)
				}
				return
			}
			if !ok || value != *test.want {
				t.Fatalf("timings=%v ok=%v want=%v body=%s", value, ok, *test.want, string(got))
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }

func TestManagementPlaygroundProxyRejectsNonPost(t *testing.T) {
	called := false
	handler := NewManagementPlaygroundProxy(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/playground/chat/completions", nil))
	if w.Code != http.StatusMethodNotAllowed || called {
		t.Fatalf("non-POST bridge request status=%d called=%v", w.Code, called)
	}
}
