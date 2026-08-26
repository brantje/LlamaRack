package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/instances"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func TestSharedStartupSurvivesFirstWaiterCancellation(t *testing.T) {
	ctx := context.Background()
	s, ms, m, sup, exec := setupLifecycle(t, true, false)
	modelInstances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(modelInstances) != 1 {
		t.Fatalf("instances=%+v err=%v", modelInstances, err)
	}
	instanceID := modelInstances[0].ID
	exec(`INSERT INTO instance_options(instance_id, option_key, option_value) VALUES(?,?,?)`, instanceID, "test-ready-delay-ms", "800")

	firstCtx, firstCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer firstCancel()
	firstDone := make(chan error, 1)
	go func() {
		_, err := s.EnsureReady(firstCtx, instanceID)
		firstDone <- err
	}()
	waitForRuntimeState(t, sup, instanceID, supervisor.Loading)

	secondCtx, secondCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer secondCancel()
	endpoint, err := s.EnsureReady(secondCtx, instanceID)
	if err != nil {
		t.Fatalf("second waiter should survive first cancellation: %v", err)
	}
	if endpoint == "" {
		t.Fatal("second waiter returned empty endpoint")
	}
	if err := <-firstDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first waiter error=%v want deadline exceeded", err)
	}
	if got := sup.Status(instanceID); got.State != supervisor.Ready {
		t.Fatalf("shared startup did not remain ready: %+v", got)
	}
}

func TestSiblingInstancesStartIndependentlyForSameModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s, _, m, sup, _ := setupLifecycle(t, true, false)
	enabled, autoload, eviction := true, true, true

	slow, err := s.Instances().Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "Slow sibling", Enabled: &enabled, Autoload: &autoload,
		EvictionEnabled: &eviction, Options: map[string]string{"test-ready-delay-ms": "2500"},
	})
	if err != nil {
		t.Fatal(err)
	}
	fast, err := s.Instances().Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "Fast sibling", Enabled: &enabled, Autoload: &autoload,
		EvictionEnabled: &eviction, Options: map[string]string{"ctx-size": "4096"},
	})
	if err != nil {
		t.Fatal(err)
	}

	type startResult struct {
		endpoint string
		err      error
	}
	slowDone := make(chan startResult, 1)
	go func() {
		endpoint, err := s.EnsureReady(ctx, slow.ID)
		slowDone <- startResult{endpoint: endpoint, err: err}
	}()
	waitForRuntimeState(t, sup, slow.ID, supervisor.Loading)

	fastCtx, fastCancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer fastCancel()
	fastEndpoint, err := s.EnsureReady(fastCtx, fast.ID)
	if err != nil {
		t.Fatalf("fast sibling was blocked by slow sibling startup: %v", err)
	}
	if fastEndpoint == "" {
		t.Fatal("fast sibling returned empty endpoint")
	}
	if got := sup.Status(slow.ID); got.State == supervisor.Ready {
		t.Fatal("slow sibling unexpectedly became ready before the independent fast sibling assertion")
	}

	slowResult := <-slowDone
	if slowResult.err != nil {
		t.Fatalf("slow sibling start failed: %v", slowResult.err)
	}
	if slowResult.endpoint == "" || slowResult.endpoint == fastEndpoint {
		t.Fatalf("sibling endpoints slow=%q fast=%q", slowResult.endpoint, fastEndpoint)
	}
	slowRuntime := sup.Status(slow.ID)
	fastRuntime := sup.Status(fast.ID)
	if slowRuntime.State != supervisor.Ready || fastRuntime.State != supervisor.Ready {
		t.Fatalf("sibling states slow=%+v fast=%+v", slowRuntime, fastRuntime)
	}
	if slowRuntime.ModelID != m.ID || fastRuntime.ModelID != m.ID {
		t.Fatalf("siblings should reference same model: slow=%+v fast=%+v", slowRuntime, fastRuntime)
	}
	if slowRuntime.Port == fastRuntime.Port {
		t.Fatalf("siblings must have private independent ports: %d", slowRuntime.Port)
	}
}

func TestOperationGateIsPerInstanceAndContextAware(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, false, false)

	releaseA, err := s.acquireOperation(context.Background(), "instance-a")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseA()

	releaseB, err := s.acquireOperation(context.Background(), "instance-b")
	if err != nil {
		t.Fatalf("different instance should not share the operation gate: %v", err)
	}
	releaseB()

	blockedCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := s.acquireOperation(blockedCtx, "instance-a"); !errors.Is(err, context.Canceled) || release != nil {
		t.Fatalf("blocked gate release=%v err=%v", release != nil, err)
	}

	releaseA()
	releaseAgain, err := s.acquireOperation(context.Background(), "instance-a")
	if err != nil {
		t.Fatal(err)
	}
	releaseAgain()
}

func TestQueuedLoadRechecksManualStopAndEnabledState(t *testing.T) {
	ctx := context.Background()
	s, ms, m, sup, exec := setupLifecycle(t, true, false)
	modelInstances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(modelInstances) != 1 {
		t.Fatalf("instances=%+v err=%v", modelInstances, err)
	}
	i := modelInstances[0]

	s.markManualStop(i.ID)
	if _, err := s.startSingleFlight(ctx, i); err == nil || err.Error() != "instance manually stopped until manager restart" {
		t.Fatalf("manual-stop recheck error=%v", err)
	}
	if got := sup.Status(i.ID).State; got != supervisor.Unloaded {
		t.Fatalf("manually stopped queued load started worker: %+v", sup.Status(i.ID))
	}

	s.clearManualStop(i.ID)
	exec("UPDATE instances SET enabled=0 WHERE id=?", i.ID)
	if _, err := s.startSingleFlight(ctx, i); err == nil || err.Error() != "instance disabled" {
		t.Fatalf("enabled-state recheck error=%v", err)
	}
	if got := sup.Status(i.ID).State; got != supervisor.Unloaded {
		t.Fatalf("disabled queued load started worker: %+v", sup.Status(i.ID))
	}
}
