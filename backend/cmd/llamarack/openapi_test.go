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
		{http.MethodGet, "/api/v1/admin/service-accounts"},
		{http.MethodPost, "/api/v1/admin/service-accounts"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/v1/completions"},
		{http.MethodPost, "/v1/responses"},
		{http.MethodPost, "/v1/embeddings"},
		{http.MethodGet, "/v1/models"},
		{http.MethodGet, "/v1/models/{model}"},
		{http.MethodGet, "/v1/responses/{response_id}"},
		{http.MethodDelete, "/v1/responses/{response_id}"},
		{http.MethodGet, "/v1/responses/{response_id}/input_items"},
		{http.MethodPost, "/v1/responses/{response_id}/cancel"},
		{http.MethodPost, "/v1/responses/input_tokens"},
		{http.MethodPost, "/v1/chat/completions/input_tokens"},
		{http.MethodPost, "/v1/chat/completions/control"},
		{http.MethodPost, "/v1/rerank"},
		{http.MethodPost, "/v1/reranking"},
		{http.MethodGet, "/v1/slots"},
		{http.MethodPost, "/v1/slots/{slot_id}"},
		{http.MethodPost, "/v1/audio/transcriptions"},
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
		"x-llamarack-request-id",
		"x-llamarack-instance",
		"x-llamarack-autoloaded",
		"x-llamarack-queue-ms",
		"x-llamarack-load-ms",
		"x-llamarack-ttft-ms",
		"x-llamarack-prompt-tokens-per-second",
		"x-llamarack-generation-tokens-per-second",
	} {
		if _, ok := response.Headers[header]; !ok {
			t.Fatalf("missing documented inference header %s", header)
		}
	}
	if _, ok := response.Headers["x-llamarack-upstream-port"]; ok {
		t.Fatal("OpenAPI must not document the internal worker port")
	}
	embeddings := doc.Paths["/v1/embeddings"]["post"].Responses["200"].Headers
	if _, ok := embeddings["x-llamarack-generation-tokens-per-second"]; ok {
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
	info, _ := spec["info"].(map[string]any)
	if info["title"] != "LlamaRack API" {
		t.Fatalf("openapi title=%v", info["title"])
	}
	components, _ := spec["components"].(map[string]any)
	schemes, _ := components["securitySchemes"].(map[string]any)
	management, _ := schemes["managementBearer"].(map[string]any)
	if management["type"] != "http" || management["scheme"] != "bearer" {
		t.Fatalf("management security scheme=%v", management)
	}
	if _, ok := schemes["managerSession"]; ok {
		t.Fatal("stale cookie security scheme must not be documented")
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "@scalar/api-reference") {
		t.Fatalf("docs status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestInferenceOpenAPIDocumentsNewSurface(t *testing.T) {
	doc := newOpenAPIDocument()
	retrieve := doc.Paths["/v1/responses/{response_id}"]["get"]
	if retrieve.Security == nil || len(retrieve.Parameters) == 0 || retrieve.Parameters[0].Name != "response_id" {
		t.Fatalf("retrieve params=%+v", retrieve.Parameters)
	}
	if !strings.Contains(retrieve.Description, "request_log_mode") || !strings.Contains(retrieve.Description, "store") {
		t.Fatalf("missing persistence note: %s", retrieve.Description)
	}
	if tag := doc.Paths["/v1/rerank"]["post"].Tags[0]; tag != "llama.cpp Extensions" {
		t.Fatalf("rerank tag=%q", tag)
	}
	if tag := doc.Paths["/v1/chat/completions/control"]["post"].Tags[0]; tag != "llama.cpp Extensions" {
		t.Fatalf("control tag=%q", tag)
	}
	audio := doc.Paths["/v1/audio/transcriptions"]["post"]
	if _, ok := audio.RequestBody.Content["multipart/form-data"]; !ok {
		t.Fatalf("audio content=%v", audio.RequestBody.Content)
	}
	if _, ok := doc.Paths["/v1/responses/input_tokens"]["post"].Responses["501"]; !ok {
		t.Fatal("token count missing 501")
	}
	create := doc.Paths["/v1/responses"]["post"]
	if !strings.Contains(create.Description, "previous_response_id") {
		t.Fatalf("missing previous_response_id note: %s", create.Description)
	}
	sa := doc.Paths["/api/v1/admin/service-accounts"]["get"]
	if !strings.Contains(sa.Summary, "Full Access key") || !strings.Contains(sa.Description, "any owner") {
		t.Fatalf("service-account OpenAPI must allow Full Access keys: %+v", sa)
	}
}
