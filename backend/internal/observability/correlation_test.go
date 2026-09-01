package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestCorrelatedRequestPersistenceAndLookup(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "observability.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := New(db)
	promptTPS := 123.5
	generationTPS := 45.25
	record := RequestRecord{
		StartedAt: 1, FinishedAt: 2, InstanceID: "demo", Endpoint: "/v1/chat/completions",
		Streaming: true, StatusCode: http.StatusOK, Result: "success", PromptTokens: 10,
		GeneratedTokens: 20, TotalTokens: 30, TokensPerSecond: &generationTPS,
	}
	if err := service.RecordCorrelatedRequest(context.Background(), "lcm_test", &promptTPS, record); err != nil {
		t.Fatal(err)
	}

	got, err := service.GetRequestByRequestID(context.Background(), "lcm_test")
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestID != "lcm_test" || got.ID == 0 || got.InstanceID != "demo" || got.PromptTokensPerSecond == nil || *got.PromptTokensPerSecond != promptTPS {
		t.Fatalf("unexpected correlated record: %+v", got)
	}
	if got.GenerationTokensPerSecond == nil || *got.GenerationTokensPerSecond != generationTPS {
		t.Fatalf("generation throughput=%v", got.GenerationTokensPerSecond)
	}

	listed, err := service.ListRequests(context.Background(), RequestFilters{InstanceID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != got.ID {
		t.Fatalf("correlation did not point at normal history row: %+v", listed)
	}
}

func TestCorrelatedRequestValidationAndHandler(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "observability.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := New(db)
	if err := service.RecordCorrelatedRequest(context.Background(), "", nil, RequestRecord{}); err == nil {
		t.Fatal("expected request ID validation error")
	}
	if err := service.RecordCorrelatedRequest(context.Background(), "lcm_missing", nil, RequestRecord{}); err == nil {
		t.Fatal("expected request record validation error")
	}
	if err := service.RecordCorrelatedRequest(context.Background(), "lcm_handler", nil, RequestRecord{StartedAt: 1, FinishedAt: 2, InstanceID: "demo", Endpoint: "/v1/embeddings", StatusCode: 200}); err != nil {
		t.Fatal(err)
	}

	h := NewCorrelatedRequestHandler(service)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests/lcm_handler", nil)
	req.SetPathValue("request_id", "lcm_handler")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"request_id":"lcm_handler"`) {
		t.Fatalf("lookup status=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests/nope", nil)
	req.SetPathValue("request_id", "nope")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/observability/requests/lcm_handler", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status=%d", w.Code)
	}
}
