package lifecycle

import (
	"context"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/scheduler"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func startHostRAMVictim(t *testing.T, evictionEnabled bool) (*Service, instances.Instance, *supervisor.Supervisor) {
	t.Helper()
	ctx := context.Background()
	s, _, model, sup, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, model.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("victim instances=%+v err=%v", items, err)
	}
	victim := items[0]
	eviction := 0
	if evictionEnabled {
		eviction = 1
	}
	execDB("UPDATE instances SET eviction_enabled=? WHERE id=?", eviction, victim.ID)
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 4*testGiB, model.ID)
	execDB("INSERT INTO instance_options(instance_id, option_key, option_value) VALUES(?,?,?)", victim.ID, "n-gpu-layers", "0")
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{
		RAMTotalBytes: 16 * testGiB, RAMAvailableBytes: 16 * testGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * testGiB}},
	}}}
	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	if sup.Status(victim.ID).State != supervisor.Ready {
		t.Fatalf("victim did not become ready: %+v", sup.Status(victim.ID))
	}
	lease, ok := s.reservations.GetByInstance(victim.ID)
	if !ok || lease.State != scheduler.LeaseCommitted || lease.HostRAM < 4*testGiB {
		t.Fatalf("victim host-RAM lease=%+v ok=%v", lease, ok)
	}
	return s, victim, sup
}

func TestNormalInstanceHostRAMPressureEvictsEligibleManagedInstance(t *testing.T) {
	s, victim, sup := startHostRAMVictim(t, true)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{
		{
			RAMTotalBytes: 16 * testGiB, RAMAvailableBytes: 2 * testGiB,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * testGiB}},
		},
		{
			RAMTotalBytes: 16 * testGiB, RAMAvailableBytes: 8 * testGiB,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * testGiB}},
		},
	}}
	target := instances.Instance{ID: "host-target", GPUMode: "manual", GPUDevices: []string{"CUDA0"}}
	demand := scheduler.ResourceDemand{
		HostRAMBytes: 6 * testGiB,
		GPU:          []scheduler.GPUResourceDemand{{Bytes: testGiB}},
	}
	placement, err := s.preparePlacementWithDemand(context.Background(), target, demand, true)
	if err != nil {
		t.Fatal(err)
	}
	if !placement.Fits || len(placement.Devices) != 1 || placement.Devices[0] != "CUDA0" {
		t.Fatalf("placement=%+v", placement)
	}
	if sup.Status(victim.ID).State != supervisor.Unloaded {
		t.Fatalf("eligible host-RAM victim was not evicted: %+v", sup.Status(victim.ID))
	}
	lease, ok := s.reservations.GetByInstance(target.ID)
	if !ok || lease.HostRAM != demand.HostRAMBytes || lease.State != scheduler.LeasePending {
		t.Fatalf("target reservation=%+v ok=%v", lease, ok)
	}
}

func TestNormalInstanceHostRAMPressureFailsWithoutEligibleVictim(t *testing.T) {
	s, victim, sup := startHostRAMVictim(t, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{
		RAMTotalBytes: 16 * testGiB, RAMAvailableBytes: 2 * testGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 12 * testGiB}},
	}}}
	target := instances.Instance{ID: "host-target", GPUMode: "manual", GPUDevices: []string{"CUDA0"}}
	demand := scheduler.ResourceDemand{
		HostRAMBytes: 6 * testGiB,
		GPU:          []scheduler.GPUResourceDemand{{Bytes: testGiB}},
	}
	placement, err := s.preparePlacementWithDemand(context.Background(), target, demand, true)
	if err == nil || !strings.Contains(err.Error(), "eligible eviction victims") {
		t.Fatalf("placement=%+v err=%v", placement, err)
	}
	if sup.Status(victim.ID).State != supervisor.Ready {
		t.Fatalf("protected victim was disturbed: %+v", sup.Status(victim.ID))
	}
	if _, ok := s.reservations.GetByInstance(target.ID); ok {
		t.Fatal("failed host-RAM admission leaked a target reservation")
	}
}
