package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRecordLifecycleCounters(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	if err := s.RecordLifecycle(ctx, LifecycleAutoload, "one", 0); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{LifecycleFailedStart, LifecycleEviction, LifecycleIdleUnload} {
		if err := s.RecordLifecycle(ctx, event, "one", 0); err != nil {
			t.Fatalf("%s: %v", event, err)
		}
	}
	if err := s.RecordLifecycle(ctx, LifecycleLoad, "one", 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLifecycle(ctx, "unknown", "one", 0); err == nil {
		t.Fatal("expected unsupported lifecycle event")
	}
	counters, err := s.Counters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, counter := range counters {
		if counter.InstanceID == "one" {
			values[counter.Metric] += counter.Value
		}
	}
	if values["autoload_total"] != 0 {
		t.Fatalf("autoload should be request-attributed, got %v", values["autoload_total"])
	}
	for _, metric := range []string{"failed_start_total", "eviction_total", "idle_unload_total", "load_total"} {
		if values[metric] != 1 {
			t.Fatalf("%s=%v counters=%v", metric, values[metric], values)
		}
	}
	if values["load_duration_ms_total"] != 1500 {
		t.Fatalf("load duration=%v", values["load_duration_ms_total"])
	}
}

func TestLifecycleSummaryWindowsAutoloadsAndCountsReloadCycles(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 31, 13, 30, 0, 0, time.UTC)

	failedOutside := RequestRecord{
		StartedAt: now.Add(-2 * time.Hour).UnixMilli(), FinishedAt: now.Add(-2 * time.Hour).UnixMilli(),
		InstanceID: "qwen", Endpoint: "/v1/chat/completions", StatusCode: 503, Autoloaded: true, LoadDurationMS: 10_000,
	}
	firstLoad := RequestRecord{
		StartedAt: now.Add(-12 * time.Minute).UnixMilli(), FinishedAt: now.Add(-11 * time.Minute).UnixMilli(),
		InstanceID: "qwen", Endpoint: "/v1/chat/completions", StatusCode: 200, Autoloaded: true, LoadDurationMS: 20_000,
	}
	reloadAfterUnload := RequestRecord{
		StartedAt: now.Add(-2 * time.Minute).UnixMilli(), FinishedAt: now.Add(-90 * time.Second).UnixMilli(),
		InstanceID: "qwen", Endpoint: "/v1/chat/completions", StatusCode: 200, Autoloaded: true, LoadDurationMS: 18_000,
	}
	warm := RequestRecord{
		StartedAt: now.Add(-10 * time.Second).UnixMilli(), FinishedAt: now.UnixMilli(),
		InstanceID: "qwen", Endpoint: "/v1/chat/completions", StatusCode: 200, Autoloaded: false, LoadDurationMS: 1_000,
	}
	for _, record := range []RequestRecord{failedOutside, firstLoad, reloadAfterUnload, warm} {
		if err := s.RecordRequest(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordLifecycle(ctx, LifecycleAutoload, "qwen", 0); err != nil {
		t.Fatal(err)
	}

	all, err := s.LifecycleSummary(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if all.Autoloads != 3 || all.FailedStarts != 1 || all.LoadMS != 48_000 {
		t.Fatalf("all-time summary=%+v", all)
	}

	window, err := s.LifecycleSummary(ctx, now.Add(-15*time.Minute).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if window.Autoloads != 2 || window.FailedStarts != 0 || window.LoadMS != 38_000 {
		t.Fatalf("load→unload→load should be 2 cold starts in-window, got %+v", window)
	}

	pending := RequestRecord{
		StartedAt: now.Add(-5 * time.Second).UnixMilli(), FinishedAt: 0, Result: "pending",
		InstanceID: "qwen", Endpoint: "/v1/chat/completions", Autoloaded: true, LoadDurationMS: 12_000,
	}
	if err := s.RecordRequest(ctx, pending); err != nil {
		t.Fatal(err)
	}
	withPending, err := s.LifecycleSummary(ctx, now.Add(-15*time.Minute).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if withPending.Autoloads != 3 || withPending.FailedStarts != 0 || withPending.LoadMS != 38_000 {
		t.Fatalf("in-flight autoload should count as a cold start without a failed start, got %+v", withPending)
	}

	negative, err := s.LifecycleSummary(ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	if negative.Autoloads != 4 {
		t.Fatalf("negative since should count all-time, got %+v", negative)
	}
}

func TestObservabilitySummaryHTTPWindowsAutoloads(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	now := time.Now()
	old := RequestRecord{
		StartedAt: now.Add(-2 * time.Hour).UnixMilli(), FinishedAt: now.Add(-2 * time.Hour).UnixMilli(),
		InstanceID: "qwen", Endpoint: "/v1/chat/completions", StatusCode: 200, Autoloaded: true, LoadDurationMS: 20_000,
	}
	recent := RequestRecord{
		StartedAt: now.Add(-2 * time.Minute).UnixMilli(), FinishedAt: now.Add(-time.Minute).UnixMilli(),
		InstanceID: "qwen", Endpoint: "/v1/chat/completions", StatusCode: 200, Autoloaded: true, LoadDurationMS: 18_000,
	}
	for _, record := range []RequestRecord{old, recent} {
		if err := s.RecordRequest(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	h := NewManagementHandler(s)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/summary?window_seconds=900", nil))
	if w.Code != 200 {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"autoloads":1`) || strings.Contains(w.Body.String(), `"autoloads":2`) {
		t.Fatalf("15m window should count only the recent cold start: %s", w.Body.String())
	}
}
