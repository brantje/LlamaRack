package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestSubscribeRuntimesIncludesConfiguredUnloadedInstances(t *testing.T) {
	ctx := context.Background()
	s, ms, model, _, _ := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances=%+v", instances)
	}

	snapshot, _, cancel, err := s.SubscribeRuntimes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot[0].InstanceID != instances[0].ID || snapshot[0].ModelID != model.ID || snapshot[0].State != supervisor.Unloaded {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestSubscribeRuntimesUsesObservedSupervisorSnapshot(t *testing.T) {
	ctx, cancelCtx := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancelCtx()
	s, ms, model, _, _ := setupLifecycle(t, true, false)
	if _, err := s.StartModel(ctx, model.ID); err != nil {
		t.Fatal(err)
	}
	instances, err := ms.Instances(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, _, cancel, err := s.SubscribeRuntimes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot[0].InstanceID != instances[0].ID || snapshot[0].ModelID != model.ID || snapshot[0].State != supervisor.Ready {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}
