package lifecycle

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestMissingGGUFStartFailureIsRecoverable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, modelService, model, _, _ := setupLifecycle(t, true, false)
	instances, err := s.Instances().ListByModel(ctx, model.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID
	path, err := modelService.ModelAbsolutePath(model)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if _, err := s.StartInstance(ctx, instanceID); err == nil {
		t.Fatal("expected deleted GGUF to prevent startup")
	}
	if runtime, err := s.RuntimeInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	} else if runtime.State == supervisor.Ready {
		t.Fatalf("missing GGUF left runtime ready: %+v", runtime)
	}

	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	// An explicit operator retry is allowed to bypass the failure cooldown once
	// the underlying filesystem problem has been repaired.
	if _, err := s.StartInstance(ctx, instanceID); err != nil {
		t.Fatalf("start after restoring GGUF: %v", err)
	}
	if runtime, err := s.RuntimeInstance(ctx, instanceID); err != nil || runtime.State != supervisor.Ready {
		t.Fatalf("restored runtime=%+v err=%v", runtime, err)
	}
	if err := s.StopInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
}
