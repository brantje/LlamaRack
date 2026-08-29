package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRuntimeOpenAPIDocumentCoversCorePublicRoutes(t *testing.T) {
	doc := newOpenAPIDocument()
	required := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/models"},
		{http.MethodPost, "/api/v1/models"},
		{http.MethodGet, "/api/v1/instances"},
		{http.MethodPost, "/api/v1/instances/{id}/restart"},
		{http.MethodGet, "/api/v1/observability/requests"},
		{http.MethodGet, "/api/v1/observability/requests/{request_id}"},
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/api-keys/{id}/rotate"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/v1/completions"},
		{http.MethodPost, "/v1/responses"},
		{http.MethodPost, "/v1/embeddings"},
		{http.MethodGet, "/v1/models"},
	}
	for _, route := range required {
		if !doc.HasOperation(route.method, route.path) {
			t.Fatalf("missing OpenAPI operation %s %s", route.method, route.path)
		}
	}
	ids := doc.OperationIDs()
	if len(ids) < 60 {
		t.Fatalf("unexpectedly small OpenAPI surface: %d operations", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Fatalf("duplicate operation id %q", ids[i])
		}
	}
}

func TestInferenceOpenAPIResponseHeaders(t *testing.T) {
	doc := newOpenAPIDocument()
	operation := doc.Paths["/v1/chat/completions"]["post"]
	response := operation.Responses["200"]
	for _, header := range []string{
		"x-llamacpp-manager-request-id",
		"x-llamacpp-manager-instance",
		"x-llamacpp-manager-autoloaded",
		"x-llamacpp-manager-queue-ms",
		"x-llamacpp-manager-load-ms",
		"x-llamacpp-manager-ttft-ms",
		"x-llamacpp-manager-prompt-tokens-per-second",
		"x-llamacpp-manager-generation-tokens-per-second",
	} {
		if _, ok := response.Headers[header]; !ok {
			t.Fatalf("missing documented inference header %s", header)
		}
	}
	embeddings := doc.Paths["/v1/embeddings"]["post"].Responses["200"].Headers
	if _, ok := embeddings["x-llamacpp-manager-generation-tokens-per-second"]; ok {
		t.Fatal("embeddings should not document generation throughput")
	}
}

func TestMuxServesRuntimeOpenAPIAndScalarDocs(t *testing.T) {
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	mux := newMux(fallback, fallback, fallback)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("openapi status=%d headers=%v", w.Code, w.Header())
	}
	var spec map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Fatalf("openapi=%v", spec["openapi"])
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "@scalar/api-reference") {
		t.Fatalf("docs status=%d body=%q", w.Code, w.Body.String())
	}
}
