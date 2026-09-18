package lifecycle

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/scheduler"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestRuntimeEvictionUsesLedgerCapacityAndAlwaysOnRecovers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	s, _, model, sup, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, model.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	victim := items[0]

	full := hardware.Snapshot{
		RAMTotalBytes: 64 * testGiB, RAMAvailableBytes: 64 * testGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 16 * testGiB, UsedBytes: 0, FreeBytes: 16 * testGiB}},
	}
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{full}}
	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeState(t, sup, victim.ID, supervisor.Ready)

	execDB("UPDATE instances SET always_on=1, eviction_enabled=1, system_spillover_enabled=1 WHERE id=?", victim.ID)
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, model.ID)

	// Model a lazy-allocation worker: telemetry shows only 2 GiB used, while the
	// manager has committed a 10 GiB reservation for the running Instance.
	lazy := hardware.Snapshot{
		RAMTotalBytes: 64 * testGiB, RAMAvailableBytes: 64 * testGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 16 * testGiB, UsedBytes: 2 * testGiB, FreeBytes: 14 * testGiB}},
		Processes: []hardware.GPUProcess{{
			PID: sup.Status(victim.ID).PID, DeviceID: "CUDA0", UsedBytes: 2 * testGiB,
		}},
	}
	s.reservations.ReleaseInstance(victim.ID)
	victimLease, err := s.reservations.Acquire(scheduler.AcquireRequest{
		InstanceID: victim.ID,
		Snapshot: lazy,
		Placement: scheduler.PlacementRequest{
			RequiredBytes: 10 * testGiB, Mode: "manual", Devices: []string{"CUDA0"}, ReserveBytes: 1,
		},
	})
	if err != nil || victimLease.ID == "" || !victimLease.Placement.Fits {
		t.Fatalf("victim reservation=%+v err=%v", victimLease, err)
	}
	if err := s.reservations.Commit(victimLease.ID); err != nil {
		t.Fatal(err)
	}

	enabled := true
	target, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: model.ID, Name: "second-instance", Enabled: &enabled, Autoload: &enabled,
		GPUMode: "auto",
	})
	if err != nil {
		t.Fatal(err)
	}

	s.hardware = readySwitchHardware{
		sup: sup, victimID: victim.ID, occupied: lazy,
		freed: hardware.Snapshot{
			RAMTotalBytes: 64 * testGiB, RAMAvailableBytes: 64 * testGiB,
			GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 16 * testGiB, UsedBytes: 0, FreeBytes: 16 * testGiB}},
		},
	}

	if _, err := s.StartInstance(ctx, target.ID); err != nil {
		t.Fatalf("second Instance must evict the lease holder and start: %v", err)
	}
	if state := sup.Status(target.ID).State; state != supervisor.Ready {
		t.Fatalf("target state=%s", state)
	}
	if state := sup.Status(victim.ID).State; state != supervisor.Unloaded {
		t.Fatalf("Always-On victim was not evicted: %s", state)
	}
	if s.isManuallyStopped(victim.ID) {
		t.Fatal("resource-pressure eviction must not set manual-stop suppression")
	}
	if reason := s.resourceBlockReason(victim.ID); reason != resourcePressureReason {
		t.Fatalf("victim resource block=%q", reason)
	}
	if logs := strings.Join(s.Logs(victim.ID), "\n"); !strings.Contains(logs, "evicted for resource pressure") {
		t.Fatalf("missing resource-pressure eviction log: %s", logs)
	}

	if err := s.StopInstance(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	s.ReconcileAlwaysOn(ctx)
	waitForRuntimeState(t, sup, victim.ID, supervisor.Ready)
	if reason := s.resourceBlockReason(victim.ID); reason != "" {
		t.Fatalf("Always-On recovery left sticky resource block=%q", reason)
	}
	if lease, ok := s.reservations.GetByInstance(victim.ID); !ok || lease.State != scheduler.LeaseCommitted {
		t.Fatalf("Always-On recovery did not commit a fresh lease: %+v ok=%v", lease, ok)
	}
}
