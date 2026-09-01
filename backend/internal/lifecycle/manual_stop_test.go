package lifecycle

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func waitForRuntimeState(t *testing.T, sup *supervisor.Supervisor, instanceID string, want supervisor.State) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if got := sup.Status(instanceID).State; got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("instance %s state=%s want=%s", instanceID, sup.Status(instanceID).State, want)
}

func TestAlwaysOnManualStopPersistsUntilDemandOrLifecycleRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, ms, m, sup, _ := setupLifecycle(t, true, true)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID

	s.ReconcileAlwaysOn(ctx)
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
	if err := s.StopInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Unloaded)
	if !s.isManuallyStopped(instanceID) {
		t.Fatal("always-on instance should be manually suppressed after Stop")
	}

	s.ReconcileAlwaysOn(ctx)
	time.Sleep(150 * time.Millisecond)
	if got := sup.Status(instanceID).State; got != supervisor.Unloaded {
		t.Fatalf("reconcile restarted manually stopped instance: %+v", sup.Status(instanceID))
	}
	if _, err := s.startInstance(ctx, instanceID, false); err == nil || !strings.Contains(err.Error(), "manually stopped") {
		t.Fatalf("background start should honor manual stop, got %v", err)
	}

	if _, err := s.EnsureReady(ctx, instanceID); err != nil {
		t.Fatalf("inference demand should resume manually stopped instance: %v", err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
	if s.isManuallyStopped(instanceID) {
		t.Fatal("inference-triggered start should clear manual stop")
	}

	if err := s.StopInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Unloaded)
	if _, err := s.StartInstance(ctx, instanceID); err != nil {
		t.Fatalf("explicit start should clear manual stop: %v", err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
	if s.isManuallyStopped(instanceID) {
		t.Fatal("explicit start should clear manual stop")
	}

	if err := s.StopInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Unloaded)
	restarted := New(ms, sup)
	if restarted.isManuallyStopped(instanceID) {
		t.Fatal("manual stop must not persist across lifecycle restart")
	}
	restarted.ReconcileAlwaysOn(ctx)
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
}

func TestNonAlwaysOnStopDoesNotSuppressAutoload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	s, ms, m, sup, _ := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID
	if _, err := s.StartInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
	if err := s.StopInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Unloaded)
	if s.isManuallyStopped(instanceID) {
		t.Fatal("non-always-on instances should not get the always-on stop override")
	}
	if _, err := s.EnsureReady(ctx, instanceID); err != nil {
		t.Fatalf("autoload should remain available for non-always-on instance: %v", err)
	}
	waitForRuntimeState(t, sup, instanceID, supervisor.Ready)
	if err := s.StopInstance(ctx, "missing-instance"); err == nil {
		t.Fatal("missing instance stop should fail")
	}
}
