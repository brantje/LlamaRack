package lifecycle

import (
	"context"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func TestAlwaysOnResourceBlockWaitsForCapacityThenReconciles(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	victim := items[0]
	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	execDB("UPDATE instances SET always_on=1 WHERE id=?", victim.ID)
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 2*testGiB, m.ID)
	if err := s.evictInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	if state := sup.Status(victim.ID).State; state != supervisor.Unloaded {
		t.Fatalf("post-eviction state=%s", state)
	}
	if reason := s.resourceBlockReason(victim.ID); reason != resourcePressureReason {
		t.Fatalf("post-eviction resource block=%q", reason)
	}
	if s.isManuallyStopped(victim.ID) {
		t.Fatal("resource-pressure eviction must not set manual-stop suppression")
	}

	// While another resource-aware start is still in progress, do not consume
	// the capacity it just freed even if the snapshot momentarily looks large.
	gapHardware := &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 3 * testGiB}}}}}
	s.hardware = gapHardware
	s.beginResourceStart()
	s.reconcileResourceBlocked(victim.ID)
	s.endResourceStart()
	if gapHardware.calls != 0 {
		t.Fatalf("blocked victim probed hardware during active resource start: calls=%d", gapHardware.calls)
	}
	if state := sup.Status(victim.ID).State; state != supervisor.Unloaded {
		t.Fatalf("blocked victim restarted during resource-start gap: %s", state)
	}

	// Automatic reconciliation probes without evicting anything. Insufficient
	// capacity keeps the desired-running Instance explicitly resource-blocked.
	fake := &sequenceHardware{snapshots: []hardware.Snapshot{
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: testGiB}}},
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 3 * testGiB}}},
	}}
	s.hardware = fake
	s.reconcileResourceBlocked(victim.ID)
	if state := sup.Status(victim.ID).State; state != supervisor.Unloaded {
		t.Fatalf("victim should remain unloaded while capacity is insufficient: %s", state)
	}
	if reason := s.resourceBlockReason(victim.ID); reason != resourcePressureReason {
		t.Fatalf("resource block cleared too early: %q", reason)
	}

	// Once capacity is actually available, the same reconciliation path starts
	// the Always-On Instance without selecting an eviction victim.
	s.reconcileResourceBlocked(victim.ID)
	if state := sup.Status(victim.ID).State; state != supervisor.Ready {
		t.Fatalf("victim did not reconcile after capacity returned: %+v", sup.Status(victim.ID))
	}
	if reason := s.resourceBlockReason(victim.ID); reason != "" {
		t.Fatalf("resource block was not cleared after successful reconcile: %q", reason)
	}
}

func TestManualStopReplacesResourcePressureBlock(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	victim := items[0]
	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	execDB("UPDATE instances SET always_on=1 WHERE id=?", victim.ID)
	if err := s.evictInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.StopInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	if reason := s.resourceBlockReason(victim.ID); reason != "" {
		t.Fatalf("manual stop should clear resource-pressure reason, got %q", reason)
	}
	if !s.isManuallyStopped(victim.ID) {
		t.Fatal("manual stop suppression was not recorded")
	}

	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 100 * testGiB}}}}}
	s.ReconcileAlwaysOn(ctx)
	if state := sup.Status(victim.ID).State; state != supervisor.Unloaded {
		t.Fatalf("manual-stop-suppressed Always-On Instance restarted: %s", state)
	}
}

func TestResourceBlockedReconcileClearsStaleReasons(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]

	// A block for an Instance that no longer exists is stale and must disappear.
	s.markResourceBlock("missing-instance")
	s.reconcileResourceBlocked("missing-instance")
	if reason := s.resourceBlockReason("missing-instance"); reason != "" {
		t.Fatalf("missing Instance retained resource block: %q", reason)
	}

	// Disabled/non-Always-On Instances are no longer desired-running, so their
	// resource-pressure block must not survive reconciliation.
	execDB("UPDATE instances SET enabled=0, always_on=1 WHERE id=?", instance.ID)
	s.markResourceBlock(instance.ID)
	s.reconcileResourceBlocked(instance.ID)
	if reason := s.resourceBlockReason(instance.ID); reason != "" {
		t.Fatalf("disabled Instance retained resource block: %q", reason)
	}

	// A running endpoint proves capacity has already been recovered. Reconcile
	// should clear a stale block without attempting another start.
	execDB("UPDATE instances SET enabled=1, always_on=1 WHERE id=?", instance.ID)
	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	s.markResourceBlock(instance.ID)
	s.reconcileResourceBlocked(instance.ID)
	if reason := s.resourceBlockReason(instance.ID); reason != "" {
		t.Fatalf("ready Instance retained resource block: %q", reason)
	}
	if state := sup.Status(instance.ID).State; state != supervisor.Ready {
		t.Fatalf("ready Instance changed state during stale-block cleanup: %s", state)
	}

	// ReconcileAlwaysOn has the same stale-block cleanup guarantee for an
	// already-running endpoint.
	s.markResourceBlock(instance.ID)
	s.ReconcileAlwaysOn(ctx)
	if reason := s.resourceBlockReason(instance.ID); reason != "" {
		t.Fatalf("Always-On reconciliation retained ready resource block: %q", reason)
	}

	// A manual stop wins over a resource-pressure retry. The block remains for
	// the caller that owns the manual-stop state to clear, and no start occurs.
	s.markManualStop(instance.ID)
	s.markResourceBlock(instance.ID)
	s.reconcileResourceBlocked(instance.ID)
	if reason := s.resourceBlockReason(instance.ID); reason != resourcePressureReason {
		t.Fatalf("manual-stop-suppressed reconcile changed resource block: %q", reason)
	}
	s.clearManualStop(instance.ID)
	s.clearResourceBlock(instance.ID)

	// Configuration/process errors are not capacity problems. Once the running
	// worker is stopped, an unsafe Model path forces a non-resource start error;
	// reconciliation must clear the stale pressure reason so ordinary retry/error
	// handling can take over.
	if err := sup.Stop(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	execDB("UPDATE models SET gguf_path='../outside.gguf' WHERE id=?", m.ID)
	s.markResourceBlock(instance.ID)
	s.reconcileResourceBlocked(instance.ID)
	if reason := s.resourceBlockReason(instance.ID); reason != "" {
		t.Fatalf("non-resource start error retained pressure block: %q", reason)
	}
}
