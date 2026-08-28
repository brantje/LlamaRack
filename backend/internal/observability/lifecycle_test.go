package observability

import (
	"context"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
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

func TestClassifyLifecycleTransitions(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	state := lifecycle.OperationalState{Activity:lifecycle.Activity{ActiveRequests:1, LastUsed:now.Add(-time.Minute)}}

	transitions := classifyLifecycle(
		supervisor.Runtime{InstanceID:"one", State:supervisor.Unloaded},
		supervisor.Runtime{InstanceID:"one", State:supervisor.Starting}, state,
	)
	if len(transitions) != 1 || transitions[0].Event != LifecycleAutoload { t.Fatalf("autoload=%+v", transitions) }

	transitions = classifyLifecycle(
		supervisor.Runtime{InstanceID:"one", State:supervisor.Loading},
		supervisor.Runtime{InstanceID:"one", State:supervisor.Ready, StartedAt:now.Add(-2*time.Second), ReadyAt:now}, state,
	)
	if len(transitions) != 1 || transitions[0].Event != LifecycleLoad || transitions[0].Duration != 2*time.Second { t.Fatalf("load=%+v", transitions) }

	transitions = classifyLifecycle(
		supervisor.Runtime{InstanceID:"one", State:supervisor.Starting},
		supervisor.Runtime{InstanceID:"one", State:supervisor.Failed}, state,
	)
	if len(transitions) != 1 || transitions[0].Event != LifecycleFailedStart { t.Fatalf("failed=%+v", transitions) }

	if got := classifyLifecycle(supervisor.Runtime{InstanceID:"one", State:supervisor.Ready}, supervisor.Runtime{InstanceID:"one", State:supervisor.Stopping}, state); len(got) != 0 {
		t.Fatalf("stop transitions must be recorded by lifecycle call sites, got=%+v", got)
	}
	if got := classifyLifecycle(supervisor.Runtime{InstanceID:"one", State:supervisor.Ready}, supervisor.Runtime{InstanceID:"one", State:supervisor.Ready}, state); len(got) != 0 { t.Fatalf("same state=%+v", got) }
	if got := classifyLifecycle(supervisor.Runtime{InstanceID:"one", State:supervisor.Ready}, supervisor.Runtime{State:supervisor.Unloaded}, state); len(got) != 0 { t.Fatalf("missing instance id=%+v", got) }

	zeroDuration := classifyLifecycle(
		supervisor.Runtime{InstanceID:"one", State:supervisor.Loading},
		supervisor.Runtime{InstanceID:"one", State:supervisor.Ready, StartedAt:now, ReadyAt:now.Add(-time.Second)}, state,
	)
	if len(zeroDuration) != 1 || zeroDuration[0].Duration != 0 { t.Fatalf("invalid timestamps=%+v", zeroDuration) }
}
