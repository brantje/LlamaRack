package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCorrelatedLifecycleAllowsUnresolvedRequestsAndFinalizesOnce(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	started := time.Now().Add(-time.Second).UnixMilli()
	record := RequestRecord{StartedAt: started, Endpoint: "/v1/chat/completions", TraceID: "aee4ef30-0d78-40a5-b71c-ef0d9d04f47f", CallType: "chat_completion", ClientIP: "203.0.113.3", UserAgent: "test-agent"}
	if err := s.BeginCorrelatedRequest(ctx, "lcm_early", record); err != nil {
		t.Fatal(err)
	}
	pending, err := s.GetRequestByRequestID(ctx, "lcm_early")
	if err != nil {
		t.Fatal(err)
	}
	if pending.InstanceID != "" || pending.Result != "pending" || pending.TraceID != record.TraceID {
		t.Fatalf("pending=%+v", pending)
	}

	record.InstanceID = "coder"
	record.APIKey = &APIKeyRef{ID: "key-1", Name: "primary", Prefix: "pk_test"}
	record.StatusCode = http.StatusServiceUnavailable
	record.Result = "error"
	record.Error = "worker unavailable"
	record.FinishedAt = time.Now().UnixMilli()
	record.DurationMS = 1000
	record.Autoloaded = true
	record.LoadDurationMS = 250
	if err := s.UpdateCorrelatedRequest(ctx, "lcm_early", record); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeCorrelatedRequest(ctx, "lcm_early", nil, record); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeCorrelatedRequest(ctx, "lcm_early", nil, record); err != nil {
		t.Fatal(err)
	}

	items, err := s.ListRequests(ctx, RequestFilters{RequestID: "lcm_early", Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].InstanceID != "coder" || items[0].ClientIP != "203.0.113.3" || items[0].APIKey == nil {
		t.Fatalf("item=%+v", items[0])
	}
	counters, err := s.Counters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var requests, autoloads float64
	for _, counter := range counters {
		if counter.Metric == "gateway_requests_total" && counter.InstanceID == "coder" {
			requests += counter.Value
		}
		if counter.Metric == "autoload_total" && counter.InstanceID == "coder" {
			autoloads += counter.Value
		}
	}
	if requests != 1 || autoloads != 1 {
		t.Fatalf("counters requests=%v autoloads=%v all=%+v", requests, autoloads, counters)
	}
}

func TestFinalizeRecoversWhenEarlyInsertWasUnavailable(t *testing.T) {
	s := testService(t)
	record := RequestRecord{StartedAt: 1, FinishedAt: 2, Endpoint: "/v1/embeddings", StatusCode: 200, Result: "success", TraceID: "11111111-2222-4333-8444-555555555555", CallType: "embedding"}
	if err := s.FinalizeCorrelatedRequest(context.Background(), "lcm_recovery", nil, record); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRequestByRequestID(context.Background(), "lcm_recovery")
	if err != nil || got.InstanceID != "" || got.CallType != "embedding" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestTraceFilteringIsChronologicalAndRequestSearchWorks(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	traceID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	for index, started := range []int64{300, 100, 200} {
		id := []string{"lcm_three", "lcm_one", "lcm_two"}[index]
		record := RequestRecord{StartedAt: started, FinishedAt: started + 1, InstanceID: "coder", Endpoint: "/v1/completions", StatusCode: 200, Result: "success", TraceID: traceID, CallType: "completion"}
		if err := s.RecordCorrelatedRequest(ctx, id, nil, record); err != nil {
			t.Fatal(err)
		}
	}
	trace, err := s.ListRequests(ctx, RequestFilters{TraceID: traceID, Limit: 10})
	if err != nil || len(trace) != 3 {
		t.Fatalf("trace=%+v err=%v", trace, err)
	}
	if trace[0].RequestID != "lcm_one" || trace[1].RequestID != "lcm_two" || trace[2].RequestID != "lcm_three" {
		t.Fatalf("trace order=%+v", trace)
	}
	search, err := s.ListRequests(ctx, RequestFilters{Search: "lcm_two", Limit: 10})
	if err != nil || len(search) != 1 || search[0].RequestID != "lcm_two" {
		t.Fatalf("search=%+v err=%v", search, err)
	}
}

func TestListJSONExcludesFullBodiesButDetailReturnsThem(t *testing.T) {
	s := testService(t)
	requestBody := `{"model":"coder","messages":[{"role":"user","content":"secret prompt"}]}`
	responseBody := `{"choices":[{"message":{"role":"assistant","content":"secret response"}}]}`
	record := RequestRecord{StartedAt: 1, FinishedAt: 2, InstanceID: "coder", Endpoint: "/v1/chat/completions", StatusCode: 200, Result: "success", RequestBody: &requestBody, ResponseBody: &responseBody}
	if err := s.RecordCorrelatedRequest(context.Background(), "lcm_full", nil, record); err != nil {
		t.Fatal(err)
	}

	items, err := s.ListRequests(context.Background(), RequestFilters{RequestID: "lcm_full", Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("list items=%+v err=%v", items, err)
	}
	if items[0].RequestBody != nil || items[0].ResponseBody != nil {
		t.Fatalf("history materialized full content: %+v", items[0])
	}

	h := NewManagementHandler(s)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests?request_id=lcm_full&limit=10", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list=%d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret prompt") || strings.Contains(w.Body.String(), "request_body") || strings.Contains(w.Body.String(), "response_body") {
		t.Fatalf("list leaked content: %s", w.Body.String())
	}

	detail := NewCorrelatedRequestHandler(s)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests/lcm_full", nil)
	req.SetPathValue("request_id", "lcm_full")
	w = httptest.NewRecorder()
	detail.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "secret prompt") || !strings.Contains(w.Body.String(), "secret response") {
		t.Fatalf("detail=%d %s", w.Code, w.Body.String())
	}
}

func TestRequestFilterParsingIncludesLogsFieldsAndPagination(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?trace_id=trace&request_id=req&search=needle&offset=25&limit=25&result=error", nil)
	filters, err := parseFilters(r)
	if err != nil {
		t.Fatal(err)
	}
	if filters.TraceID != "trace" || filters.RequestID != "req" || filters.Search != "needle" || filters.Offset != 25 || filters.Limit != 25 || filters.Result != "error" {
		t.Fatalf("filters=%+v", filters)
	}
	for _, raw := range []string{"/?offset=-1", "/?result=pending"} {
		if _, err := parseFilters(httptest.NewRequest(http.MethodGet, raw, nil)); err == nil {
			t.Fatalf("expected error for %s", raw)
		}
	}
}

func TestRequestListResponseHasPaginationMetadata(t *testing.T) {
	s := testService(t)
	record := RequestRecord{StartedAt: 1, FinishedAt: 2, InstanceID: "coder", Endpoint: "/v1/completions", StatusCode: 200}
	if err := s.RecordCorrelatedRequest(context.Background(), "lcm_page", nil, record); err != nil {
		t.Fatal(err)
	}
	record.StartedAt, record.FinishedAt = 3, 4
	if err := s.RecordCorrelatedRequest(context.Background(), "lcm_page_2", nil, record); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	NewManagementHandler(s).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/requests?limit=1&offset=0", nil))
	var payload struct {
		Items   []RequestRecord `json:"items"`
		Limit   int             `json:"limit"`
		Offset  int             `json:"offset"`
		HasMore bool            `json:"has_more"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(payload.Items) != 1 || payload.Limit != 1 || payload.Offset != 0 || !payload.HasMore {
		t.Fatalf("payload=%+v status=%d", payload, w.Code)
	}
}
