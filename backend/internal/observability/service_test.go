package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db)
}

func floatp(value float64) *float64 { return &value }
func stringp(value string) *string  { return &value }

func TestActivityTransitions(t *testing.T) {
	s := testService(t)
	s.Queue("one")
	s.Queue("one")
	s.Queue("two")
	s.Activate("one")
	active, queued := s.Activity()
	if active["one"] != 1 || queued["one"] != 1 || queued["two"] != 1 {
		t.Fatalf("activity active=%v queued=%v", active, queued)
	}
	s.EndActive("one")
	s.EndActive("one")
	s.EndQueued("one")
	s.EndQueued("one")
	s.EndQueued("two")
	active, queued = s.Activity()
	if len(active) != 0 || len(queued) != 0 {
		t.Fatalf("activity should be empty: %v %v", active, queued)
	}
}

func TestRecordSummaryListCountersTimeseriesAndPrune(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if err := s.RecordRequest(ctx, RequestRecord{InstanceID: "", Endpoint: "/v1/test"}); err == nil {
		t.Fatal("expected validation error")
	}

	first := RequestRecord{
		StartedAt: now.Add(-2 * time.Minute).UnixMilli(), FinishedAt: now.Add(-119 * time.Second).UnixMilli(), InstanceID: "coder", Endpoint: "/v1/chat/completions",
		APIKey: &APIKeyRef{ID: "key-1", Name: "primary", Prefix: "abc123"}, Streaming: true, StatusCode: 200, DurationMS: 1000, TTFTMS: floatp(120),
		PromptTokens: 10, GeneratedTokens: 20, TotalTokens: 30, TokensPerSecond: floatp(25), QueueDurationMS: 50, LoadDurationMS: 50, Autoloaded: true,
		RequestBody: stringp(`{"model":"coder"}`), ResponseBody: stringp(`{"ok":true}`),
	}
	if err := s.RecordRequest(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := RequestRecord{
		StartedAt: now.Add(-time.Minute).UnixMilli(), FinishedAt: now.Add(-59 * time.Second).UnixMilli(), InstanceID: "coder", Endpoint: "/v1/embeddings",
		APIKey: &APIKeyRef{ID: "key-2", Name: "embed", Prefix: "def456"}, StatusCode: 503, Result: "error", DurationMS: 2000, TTFTMS: floatp(300), Error: "worker unavailable",
	}
	if err := s.RecordRequest(ctx, second); err != nil {
		t.Fatal(err)
	}

	s.Queue("coder")
	s.Activate("coder")
	summary, err := s.Summary(ctx, now.Add(-15*time.Minute).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Requests != 2 || summary.Successes != 1 || summary.Errors != 1 || summary.ActiveAPIKeys != 2 || summary.Active != 1 || summary.Queued != 0 {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.PromptTokens != 10 || summary.GeneratedTokens != 20 || summary.TotalTokens != 30 {
		t.Fatalf("token summary=%+v", summary)
	}
	if summary.LatencyMS.P50 == nil || *summary.LatencyMS.P50 != 1000 || summary.LatencyMS.P99 == nil || *summary.LatencyMS.P99 != 2000 {
		t.Fatalf("latency=%+v", summary.LatencyMS)
	}
	if summary.TTFTMS.P95 == nil || *summary.TTFTMS.P95 != 300 {
		t.Fatalf("ttft=%+v", summary.TTFTMS)
	}
	s.EndActive("coder")

	streaming := true
	items, err := s.ListRequests(ctx, RequestFilters{InstanceID: "coder", APIKeyID: "key-1", Result: "success", StatusCode: 200, Streaming: &streaming, Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if items[0].RequestBody != nil || items[0].ResponseBody != nil || items[0].APIKey == nil || items[0].APIKey.Prefix != "abc123" || !items[0].Autoloaded {
		t.Fatalf("record=%+v", items[0])
	}
	items, err = s.ListRequests(ctx, RequestFilters{Endpoint: "/v1/embeddings", BeforeMS: now.UnixMilli(), SinceMS: now.Add(-5 * time.Minute).UnixMilli(), Limit: 1000})
	if err != nil || len(items) != 1 || items[0].Error == "" {
		t.Fatalf("filtered=%+v err=%v", items, err)
	}

	counters, err := s.Counters(ctx)
	if err != nil || len(counters) < 4 {
		t.Fatalf("counters=%+v err=%v", counters, err)
	}
	foundRequests := 0
	for _, counter := range counters {
		if counter.Metric == "gateway_requests_total" {
			foundRequests++
		}
	}
	if foundRequests != 2 {
		t.Fatalf("request counters=%d %+v", foundRequests, counters)
	}

	points, err := s.Timeseries(ctx, "requests", now.Add(-15*time.Minute).UnixMilli(), 60)
	if err != nil || len(points) != 2 {
		t.Fatalf("request points=%+v err=%v", points, err)
	}
	for _, metric := range []string{"latency", "ttft", "tokens"} {
		if _, err := s.Timeseries(ctx, metric, now.Add(-15*time.Minute).UnixMilli(), 999999); err != nil {
			t.Fatalf("metric %s: %v", metric, err)
		}
	}
	if _, err := s.Timeseries(ctx, "bogus", 0, 0); err == nil {
		t.Fatal("expected unsupported metric")
	}

	old := RequestRecord{StartedAt: now.Add(-40 * 24 * time.Hour).UnixMilli(), FinishedAt: now.Add(-40 * 24 * time.Hour).UnixMilli(), InstanceID: "old", Endpoint: "/v1/completions", StatusCode: 200, DurationMS: 1}
	if err := s.RecordRequest(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, 30); err != nil {
		t.Fatal(err)
	}
	oldItems, err := s.ListRequests(ctx, RequestFilters{InstanceID: "old"})
	if err != nil || len(oldItems) != 0 {
		t.Fatalf("old items=%v err=%v", oldItems, err)
	}
}

func TestSummaryDefaultsAndEmptyPercentiles(t *testing.T) {
	s := testService(t)
	summary, err := s.Summary(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Requests != 0 || summary.LatencyMS.P50 != nil || summary.TTFTMS.P99 != nil {
		t.Fatalf("summary=%+v", summary)
	}
	if got := percentiles(nil); got.P50 != nil {
		t.Fatalf("percentiles=%+v", got)
	}
}

func TestManagementHandler(t *testing.T) {
	s := testService(t)
	now := time.Now().UTC()
	if err := s.RecordRequest(context.Background(), RequestRecord{StartedAt: now.UnixMilli(), FinishedAt: now.UnixMilli(), InstanceID: "one", Endpoint: "/v1/completions", StatusCode: 200, DurationMS: 10}); err != nil {
		t.Fatal(err)
	}
	h := NewManagementHandler(s)
	for _, path := range []string{
		"/api/v1/observability/summary?window_seconds=60",
		"/api/v1/observability/requests?instance_id=one&limit=10",
		"/api/v1/observability/timeseries?metric=requests&bucket_seconds=30",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 200 {
			t.Fatalf("%s => %d %s", path, w.Code, w.Body.String())
		}
	}
	bad := []string{
		"/api/v1/observability/summary?since=nope",
		"/api/v1/observability/summary?window_seconds=0",
		"/api/v1/observability/requests?status_code=99",
		"/api/v1/observability/requests?streaming=maybe",
		"/api/v1/observability/requests?limit=501",
		"/api/v1/observability/timeseries?bucket_seconds=zero",
		"/api/v1/observability/timeseries?metric=nope",
	}
	for _, path := range bad {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 400 {
			t.Fatalf("%s => %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/observability/summary", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/nope", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("not found=%d", w.Code)
	}
}

func TestMetricsHandlerAuthAndLabels(t *testing.T) {
	s := testService(t)
	now := time.Now().UTC()
	if err := s.RecordRequest(context.Background(), RequestRecord{StartedAt: now.UnixMilli(), FinishedAt: now.UnixMilli(), InstanceID: `one"x`, Endpoint: "/v1/completions", StatusCode: 200, DurationMS: 100, TTFTMS: floatp(10), PromptTokens: 2, GeneratedTokens: 3, TotalTokens: 5}); err != nil {
		t.Fatal(err)
	}
	s.Queue("one")
	open := NewMetricsHandler(s, func(context.Context) string { return "" })
	w := httptest.NewRecorder()
	open.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "llamarack_gateway_requests_total") || strings.Contains(w.Body.String(), "llamacpp_manager_gateway_requests_total") || !strings.Contains(w.Body.String(), `instance_id="one\"x"`) || !strings.Contains(w.Body.String(), "request_latency_seconds") {
		t.Fatalf("metrics=%d %s", w.Code, w.Body.String())
	}
	protected := NewMetricsHandler(s, func(context.Context) string { return "secret" })
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != 401 {
		t.Fatalf("unauth=%d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("auth=%d", w.Code)
	}
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", w.Code)
	}
}

func TestQueueLimitRejectionMetricAndPendingLimitGauges(t *testing.T) {
	s := testService(t)
	s.SetPendingLimits(func(context.Context) (int, int) { return 7, 9 })
	if err := s.RecordQueueLimitRejection(context.Background(), "coder", "instance"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordQueueLimitRejection(context.Background(), "coder", "global"); err != nil {
		t.Fatal(err)
	}
	h := NewMetricsHandler(s, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := w.Body.String()
	if !strings.Contains(body, `llamarack_gateway_queue_limit_rejections_total{instance_id="coder",limit="instance"} 1`) {
		t.Fatalf("missing instance rejection metric: %s", body)
	}
	if !strings.Contains(body, `llamarack_gateway_queue_limit_rejections_total{instance_id="coder",limit="global"} 1`) {
		t.Fatalf("missing global rejection metric: %s", body)
	}
	if !strings.Contains(body, `llamarack_gateway_pending_request_limit{scope="instance"} 7`) || !strings.Contains(body, `llamarack_gateway_pending_request_limit{scope="global"} 9`) {
		t.Fatalf("missing pending limit gauges: %s", body)
	}
	if err := s.RecordQueueLimitRejection(context.Background(), " ", "instance"); err == nil {
		t.Fatal("expected instance_id validation error")
	}
	if err := s.RecordQueueLimitRejection(context.Background(), "coder", "unknown"); err != nil {
		t.Fatal(err)
	}
	blank := testService(t)
	perInstance, global := blank.PendingLimits(context.Background())
	if perInstance != 32 || global != 128 {
		t.Fatalf("default pending limits=%d %d", perInstance, global)
	}
}

func TestJSONAndParsingHelpers(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, 201, map[string]bool{"ok": true})
	if w.Code != 201 {
		t.Fatal(w.Code)
	}
	var value map[string]bool
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil || !value["ok"] {
		t.Fatalf("json=%v err=%v", value, err)
	}
	r := httptest.NewRequest(http.MethodGet, "/?since=123&before=456&status_code=200&streaming=true&limit=3", nil)
	filters, err := parseFilters(r)
	if err != nil || filters.SinceMS != 123 || filters.BeforeMS != 456 || filters.StatusCode != 200 || filters.Streaming == nil || !*filters.Streaming || filters.Limit != 3 {
		t.Fatalf("filters=%+v err=%v", filters, err)
	}
	if metricOrDefault("") != "requests" || metricOrDefault("tokens") != "tokens" {
		t.Fatal("metric default")
	}
	if got := promEscape("a\\b\nc\""); got != `a\\b\nc\"` {
		t.Fatalf("escape=%q", got)
	}
}

func TestRunRetentionStopsWithContext(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { s.RunRetention(ctx, func(context.Context) int { return 1 }); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retention did not stop")
	}
}
