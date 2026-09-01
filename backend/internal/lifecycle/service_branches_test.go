package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func slowLifecycleBinary(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LCM_LIFECYCLE_TEST_BINARY", exe)
	t.Setenv("GO_WANT_LIFECYCLE_HELPER", "1")
	path := filepath.Join(t.TempDir(), "slow-llama")
	script := "#!/bin/sh\nsleep 1\nexec \"$LCM_LIFECYCLE_TEST_BINARY\" -test.run=TestLifecycleHelperProcess -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStartOneManualGPUAndModelPathEscape(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s, _, m, _, exec := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{}}}
	exec("UPDATE instances SET gpu_mode='manual',gpu_devices='0,1',tensor_split='1,1' WHERE model_id=?", m.ID)
	if _, err := s.StartModel(ctx, m.ID); err != nil {
		t.Fatalf("manual GPU start: %v", err)
	}
	if err := s.StopModel(ctx, m.ID); err != nil {
		t.Fatal(err)
	}

	exec("UPDATE models SET gguf_path='../escape.gguf' WHERE id=?", m.ID)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%v err=%v", items, err)
	}
	if _, err := s.startOne(ctx, items[0]); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected path escape error, got %v", err)
	}
}

func TestSingleFlightWaiterHonorsContextCancellation(t *testing.T) {
	s, _, m, oldSup, _ := setupLifecycle(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = oldSup
	slowSup := supervisor.New(slowLifecycleBinary(t), "127.0.0.1", 30000, 4*time.Second)
	s.sup = slowSup
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		slowSup.Shutdown(stopCtx)
	})
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%v err=%v", items, err)
	}
	instanceID := items[0].ID

	firstDone := make(chan error, 1)
	go func() {
		_, err := s.StartInstance(ctx, instanceID)
		firstDone <- err
	}()

	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		loading := s.loads[instanceID] != nil
		s.mu.Unlock()
		if loading {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("load never entered single-flight map")
		}
		time.Sleep(5 * time.Millisecond)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer waitCancel()
	if _, err := s.StartInstance(waitCtx, instanceID); err == nil || err != context.DeadlineExceeded {
		t.Fatalf("waiter error=%v", err)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first load failed: %v", err)
	}
}

func TestReconcileSkipsDisabledAndAlreadyReady(t *testing.T) {
	ctx := context.Background()
	s, ms, m, sup, exec := setupLifecycle(t, true, true)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	exec("UPDATE instances SET enabled=0 WHERE model_id=?", m.ID)
	s.ReconcileAlwaysOn(ctx)
	if got := sup.Status(instances[0].ID); got.State != supervisor.Unloaded {
		t.Fatalf("disabled always-on started: %+v", got)
	}
	exec("UPDATE instances SET enabled=1 WHERE model_id=?", m.ID)
	s.ReconcileAlwaysOn(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && sup.Status(instances[0].ID).State != supervisor.Ready {
		time.Sleep(10 * time.Millisecond)
	}
	if sup.Status(instances[0].ID).State != supervisor.Ready {
		t.Fatal("always-on worker not ready")
	}
	s.ReconcileAlwaysOn(ctx)
	if sup.Status(instances[0].ID).State != supervisor.Ready {
		t.Fatal("ready worker changed state")
	}
}
