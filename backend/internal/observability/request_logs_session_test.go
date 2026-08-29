package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func seedSessionRequestLogs(t *testing.T) (*Service, context.Context) {
	t.Helper()
	s := testService(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,quantization,context_length) VALUES(?,?,?,?,?,?)`, "m1", "Qwen Coder 7B", "/models/coder.gguf", 1, "Q4_K_M", 4096); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO instances(id,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds) VALUES(?,?,?,?,?,?,?,?,?)`, "coder", "m1", "Coder", 1, 1, 0, "normal", 1, 0); err != nil {
		t.Fatal(err)
	}
	promptTPS := 42.5
	for index, requestID := range []string{"lcm_session_1", "lcm_session_2"} {
		requestBody := `{"model":"coder","messages":[{"role":"user","content":"hello"}]}`
		responseBody := `{"choices":[{"message":{"role":"assistant","content":"world"}}]}`
		ttft := float64(25 + index)
		generationTPS := float64(50 + index)
		record := RequestRecord{
			StartedAt: int64(1000 + index*100), FinishedAt: int64(1100 + index*100), InstanceID: "coder", Endpoint: "/v1/chat/completions",
			APIKey: &APIKeyRef{ID: "key-1", Name: "Primary key", Prefix: "pk_live"}, Streaming: index == 0, StatusCode: http.StatusOK, Result: "success",
			DurationMS: float64(100 + index), TTFTMS: &ttft, PromptTokens: 2, GeneratedTokens: 3, TotalTokens: 5, TokensPerSecond: &generationTPS,
			QueueDurationMS: 4, LoadDurationMS: 5, Autoloaded: index == 0, TraceID: "11111111-2222-4333-8444-555555555555", CallType: "chat_completion",
			ClientIP: "198.51.100.10", UserAgent: "session-test/1.0", RequestBody: &requestBody, ResponseBody: &responseBody,
		}
		if err := s.RecordCorrelatedRequest(ctx, requestID, &promptTPS, record); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateRequestLogContext(ctx, requestID, "session-abc", "coder"); err != nil {
			t.Fatal(err)
		}
	}
	return s, ctx
}

func TestSessionRequestLogPersistenceFilteringAndDetail(t *testing.T) {
	s, ctx := seedSessionRequestLogs(t)
	if err := s.EnsureRequestLogSchema(ctx); err != nil {
		t.Fatalf("idempotent schema: %v", err)
	}

	items, err := s.ListRequestLogs(ctx, RequestFilters{Limit: 25}, "session-abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%+v", items)
	}
	for _, item := range items {
		if item.SessionID != "session-abc" || item.SessionTotalCount != 2 || item.ModelID != "m1" || item.ModelName != "Qwen Coder 7B" {
			t.Fatalf("session/model context=%+v", item)
		}
		if item.APIKey == nil || item.APIKey.Name != "Primary key" || item.RequestBody != nil || item.ResponseBody != nil {
			t.Fatalf("list privacy/alias=%+v", item)
		}
		if item.PromptTokensPerSecond == nil || item.GenerationTokensPerSecond == nil || item.TTFTMS == nil {
			t.Fatalf("metrics missing=%+v", item)
		}
	}

	searched, err := s.ListRequestLogs(ctx, RequestFilters{Search: "Qwen Coder", Limit: 25}, "")
	if err != nil || len(searched) != 2 {
		t.Fatalf("model search=%+v err=%v", searched, err)
	}
	searched, err = s.ListRequestLogs(ctx, RequestFilters{Search: "session-abc", Limit: 25}, "")
	if err != nil || len(searched) != 2 {
		t.Fatalf("session search=%+v err=%v", searched, err)
	}
	streaming := true
	filtered, err := s.ListRequestLogs(ctx, RequestFilters{
		SinceMS: 900, BeforeMS: 1200, InstanceID: "coder", Endpoint: "/v1/chat/completions", APIKeyID: "key-1", Result: "success",
		StatusCode: http.StatusOK, Streaming: &streaming, RequestID: "lcm_session_1", TraceID: "11111111-2222-4333-8444-555555555555", Limit: 25,
	}, "session-abc")
	if err != nil || len(filtered) != 1 || filtered[0].RequestID != "lcm_session_1" {
		t.Fatalf("filters=%+v err=%v", filtered, err)
	}

	detail, err := s.GetRequestLogByRequestID(ctx, "lcm_session_1")
	if err != nil {
		t.Fatal(err)
	}
	if detail.RequestBody == nil || detail.ResponseBody == nil || !strings.Contains(*detail.RequestBody, "hello") || detail.SessionTotalCount != 2 {
		t.Fatalf("detail=%+v", detail)
	}
	if _, err := s.GetRequestLogByRequestID(ctx, ""); err == nil {
		t.Fatal("expected empty request id validation error")
	}
	if err := s.UpdateRequestLogContext(ctx, "", "session-abc", "coder"); err == nil {
		t.Fatal("expected empty context request id validation error")
	}
	if err := s.UpdateRequestLogContext(ctx, "missing", "session-abc", "coder"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing context update err=%v", err)
	}
}

func TestSessionRequestLogHTTPHandlers(t *testing.T) {
	s, _ := seedSessionRequestLogs(t)
	list := NewRequestLogsHandler(s)

	w := httptest.NewRecorder()
	list.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/observability/requests", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", w.Code)
	}

	w = httptest.NewRecorder()
	list.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests?status_code=nope", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad filter=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	list.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests?session_id=session-abc&limit=1", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list=%d body=%s", w.Code, w.Body.String())
	}
	var page struct {
		Items   []RequestLogRecord `json:"items"`
		HasMore bool               `json:"has_more"`
		Limit   int                `json:"limit"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.HasMore || page.Limit != 1 || page.Items[0].SessionTotalCount != 2 {
		t.Fatalf("page=%+v", page)
	}

	detailHandler := NewRequestLogDetailHandler(s)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests/lcm_session_1", nil)
	req.SetPathValue("request_id", "lcm_session_1")
	w = httptest.NewRecorder()
	detailHandler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"session_id":"session-abc"`) || !strings.Contains(w.Body.String(), `"request_body"`) {
		t.Fatalf("detail=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests/missing", nil)
	req.SetPathValue("request_id", "missing")
	w = httptest.NewRecorder()
	detailHandler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests/", nil)
	req.SetPathValue("request_id", "")
	w = httptest.NewRecorder()
	detailHandler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	detailHandler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/observability/requests/lcm_session_1", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("detail method=%d", w.Code)
	}
}
