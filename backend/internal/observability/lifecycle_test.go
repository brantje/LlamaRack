package observability

import (
	"context"
	"testing"
	"time"
)

func TestRecordLifecycleCounters(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	for _, event := range []string{LifecycleAutoload, LifecycleFailedStart, LifecycleEviction, LifecycleIdleUnload} {
		if err := s.RecordLifecycle(ctx, event, "one", 0); err != nil { t.Fatalf("%s: %v", event, err) }
	}
	if err := s.RecordLifecycle(ctx, LifecycleLoad, "one", 1500*time.Millisecond); err != nil { t.Fatal(err) }
	if err := s.RecordLifecycle(ctx, "unknown", "one", 0); err == nil { t.Fatal("expected unsupported lifecycle event") }
	counters, err := s.Counters(ctx)
	if err != nil { t.Fatal(err) }
	values := map[string]float64{}
	for _, counter := range counters {
		if counter.InstanceID == "one" { values[counter.Metric] += counter.Value }
	}
	for _, metric := range []string{"autoload_total", "failed_start_total", "eviction_total", "idle_unload_total", "load_total"} {
		if values[metric] != 1 { t.Fatalf("%s=%v counters=%v", metric, values[metric], values) }
	}
	if values["load_duration_ms_total"] != 1500 { t.Fatalf("load duration=%v", values["load_duration_ms_total"]) }
}
