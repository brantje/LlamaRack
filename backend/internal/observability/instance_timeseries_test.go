package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/telemetry"
)

func TestRequestTimeseriesScopesAndBucketsByInstance(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	now := time.Date(2026, 8, 29, 20, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	records := []RequestRecord{
		{StartedAt: now.Add(-110 * time.Second).UnixMilli(), FinishedAt: now.Add(-109 * time.Second).UnixMilli(), InstanceID: "alpha", Endpoint: "/v1/chat/completions", StatusCode: 200, DurationMS: 100, PromptTokens: 10, GeneratedTokens: 20, TotalTokens: 30},
		{StartedAt: now.Add(-100 * time.Second).UnixMilli(), FinishedAt: now.Add(-99 * time.Second).UnixMilli(), InstanceID: "alpha", Endpoint: "/v1/chat/completions", StatusCode: 500, DurationMS: 300, PromptTokens: 5, GeneratedTokens: 15, TotalTokens: 20},
		{StartedAt: now.Add(-95 * time.Second).UnixMilli(), FinishedAt: now.Add(-94 * time.Second).UnixMilli(), InstanceID: "beta", Endpoint: "/v1/chat/completions", StatusCode: 200, DurationMS: 900, PromptTokens: 50, GeneratedTokens: 60, TotalTokens: 110},
		{StartedAt: now.Add(-30 * time.Second).UnixMilli(), FinishedAt: now.Add(-29 * time.Second).UnixMilli(), InstanceID: "alpha", Endpoint: "/v1/responses", StatusCode: 200, DurationMS: 1000, PromptTokens: 2, GeneratedTokens: 3, TotalTokens: 5},
	}
	for _, record := range records {
		if err := s.RecordRequest(ctx, record); err != nil { t.Fatal(err) }
	}

	requests, err := s.RequestTimeseries(ctx, "requests", now.Add(-5*time.Minute).UnixMilli(), 60, "alpha")
	if err != nil || len(requests) != 2 || requests[0].Value != 2 || requests[1].Value != 1 { t.Fatalf("requests=%+v err=%v", requests, err) }
	prompt, err := s.RequestTimeseries(ctx, "prompt_tokens", now.Add(-5*time.Minute).UnixMilli(), 60, "alpha")
	if err != nil || len(prompt) != 2 || prompt[0].Value != 15 || prompt[1].Value != 2 { t.Fatalf("prompt=%+v err=%v", prompt, err) }
	generated, err := s.RequestTimeseries(ctx, "generated_tokens", now.Add(-5*time.Minute).UnixMilli(), 60, "alpha")
	if err != nil || generated[0].Value != 35 { t.Fatalf("generated=%+v err=%v", generated, err) }
	p50, err := s.RequestTimeseries(ctx, "latency_p50", now.Add(-5*time.Minute).UnixMilli(), 60, "alpha")
	if err != nil || len(p50) != 2 || p50[0].Value != 100 { t.Fatalf("p50=%+v err=%v", p50, err) }
	p95, err := s.RequestTimeseries(ctx, "latency_p95", now.Add(-5*time.Minute).UnixMilli(), 60, "alpha")
	if err != nil || len(p95) != 2 || p95[0].Value != 300 { t.Fatalf("p95=%+v err=%v", p95, err) }
	if _, err := s.RequestTimeseries(ctx, "unsupported", 0, 0, "alpha"); err == nil { t.Fatal("expected unsupported metric") }
}

func TestContextHistoryPersistsOnlyDerivedGauge(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	at := time.Date(2026, 8, 29, 20, 0, 0, 0, time.UTC)
	contextMax := 4096.0
	negative := -1.0
	if err := s.RecordContextMetrics(ctx, at, []RuntimeTelemetrySample{
		{Sample: telemetry.Sample{InstanceID: "alpha", PID: 1}, LlamaMetrics: &telemetry.LlamaMetrics{ContextTokensMax: &contextMax}},
		{Sample: telemetry.Sample{InstanceID: "beta", PID: 2}, LlamaMetrics: &telemetry.LlamaMetrics{ContextTokensMax: &negative}},
		{Sample: telemetry.Sample{InstanceID: "gamma", PID: 3}},
	}); err != nil { t.Fatal(err) }

	points, err := s.RequestTimeseries(ctx, "instance_context_tokens_max", at.Add(-time.Minute).UnixMilli(), 60, "alpha")
	if err != nil || len(points) != 1 || points[0].Value != 4096 { t.Fatalf("context=%+v err=%v", points, err) }
	other, err := s.RequestTimeseries(ctx, "instance_context_tokens_max", at.Add(-time.Minute).UnixMilli(), 60, "beta")
	if err != nil || len(other) != 0 { t.Fatalf("negative context should not persist: %+v err=%v", other, err) }
}

func TestManagementTimeseriesPassesInstanceFilter(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	now := time.Now().UTC()
	for _, id := range []string{"alpha", "beta"} {
		if err := s.RecordRequest(ctx, RequestRecord{StartedAt: now.UnixMilli(), FinishedAt: now.UnixMilli(), InstanceID: id, Endpoint: "/v1/responses", StatusCode: 200, DurationMS: 10, PromptTokens: 1, GeneratedTokens: 2, TotalTokens: 3}); err != nil { t.Fatal(err) }
	}
	h := NewManagementHandler(s)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/timeseries?metric=requests&window_seconds=60&bucket_seconds=60&instance_id=alpha", nil))
	if w.Code != http.StatusOK { t.Fatalf("status=%d body=%s", w.Code, w.Body.String()) }
	var response struct { Items []SeriesPoint `json:"items"` }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil { t.Fatal(err) }
	if len(response.Items) != 1 || response.Items[0].Value != 1 { t.Fatalf("items=%+v", response.Items) }
}
