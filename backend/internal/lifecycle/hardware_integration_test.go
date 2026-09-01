package lifecycle

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

const testGiB int64 = 1024 * 1024 * 1024

type sequenceHardware struct {
	mu        sync.Mutex
	snapshots []hardware.Snapshot
	err       error
	calls     int
}

func (s *sequenceHardware) Snapshot(context.Context) (hardware.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return hardware.Snapshot{}, s.err
	}
	if len(s.snapshots) == 0 {
		return hardware.Snapshot{}, nil
	}
	index := s.calls - 1
	if index >= len(s.snapshots) {
		index = len(s.snapshots) - 1
	}
	return s.snapshots[index], nil
}

func TestPreparePlacementPrefersSingleAdequateGPU(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	fake := &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 6 * testGiB},
		{ID: "CUDA1", FreeBytes: 8 * testGiB},
	}}}}
	s.hardware = fake

	placement, err := s.preparePlacement(context.Background(), instances.Instance{ID: "target", GPUMode: "auto"}, 4*testGiB)
	if err != nil {
		t.Fatal(err)
	}
	if !placement.Fits || len(placement.Devices) != 1 || placement.Devices[0] != "CUDA1" {
		t.Fatalf("expected largest adequate single GPU, got %+v", placement)
	}
	if fake.calls != 1 {
		t.Fatalf("unexpected snapshot count: %d", fake.calls)
	}
}

func TestPreparePlacementExecutesEvictionThenRefreshesHardware(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, execDB := setupLifecycle(t, true, false)
	victims, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(victims) != 1 {
		t.Fatalf("victim instances=%+v err=%v", victims, err)
	}
	victim := victims[0]
	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	if sup.Status(victim.ID).State != supervisor.Ready {
		t.Fatalf("victim did not become ready: %+v", sup.Status(victim.ID))
	}

	// Give the already-running victim enough estimated bytes to satisfy the
	// shortfall. The refreshed hardware snapshot is still the final authority.
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 2*testGiB, m.ID)
	fake := &sequenceHardware{snapshots: []hardware.Snapshot{
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 1 * testGiB}}},
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 3 * testGiB}}},
	}}
	s.hardware = fake

	placement, err := s.preparePlacement(ctx, instances.Instance{ID: "target", GPUMode: "auto"}, 2*testGiB)
	if err != nil {
		t.Fatal(err)
	}
	if !placement.Fits || len(placement.Devices) != 1 || placement.Devices[0] != "CUDA0" {
		t.Fatalf("unexpected post-eviction placement: %+v", placement)
	}
	if fake.calls != 2 {
		t.Fatalf("expected hardware refresh after eviction, calls=%d", fake.calls)
	}
	if state := sup.Status(victim.ID).State; state != supervisor.Unloaded {
		t.Fatalf("victim was not stopped before target placement: %s", state)
	}
	if s.isManuallyStopped(victim.ID) {
		t.Fatal("resource-pressure eviction must not mark the victim manually stopped")
	}
}

func TestPreparePlacementRejectsInsufficientCapacityWithoutEligibleVictims(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: testGiB}}}}}
	_, err := s.preparePlacement(context.Background(), instances.Instance{ID: "target", GPUMode: "auto"}, 2*testGiB)
	if err == nil || !strings.Contains(err.Error(), "eligible eviction victims") {
		t.Fatalf("expected capacity error, got %v", err)
	}
}

func TestPreparePlacementManualAndDetectorFallbacks(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * testGiB}}}}}
	_, err := s.preparePlacement(context.Background(), instances.Instance{ID: "target", GPUMode: "manual", GPUDevices: []string{"CUDA9"}}, testGiB)
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected missing manual GPU error, got %v", err)
	}

	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{}}}
	placement, err := s.preparePlacement(context.Background(), instances.Instance{ID: "target", GPUMode: "auto"}, testGiB)
	if err != nil || len(placement.Devices) != 0 {
		t.Fatalf("CPU/other-backend fallback=%+v err=%v", placement, err)
	}

	s.hardware = &sequenceHardware{err: errors.New("probe unavailable")}
	placement, err = s.preparePlacement(context.Background(), instances.Instance{ID: "target", GPUMode: "auto"}, testGiB)
	if err != nil || len(placement.Devices) != 0 {
		t.Fatalf("detector error compatibility fallback=%+v err=%v", placement, err)
	}
}

func TestEvictInstanceRevalidatesEligibility(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	victim := items[0]
	if err := s.evictInstance(ctx, victim.ID); err == nil || !strings.Contains(err.Error(), "no longer eligible") {
		t.Fatalf("unloaded victim should be rejected: %v", err)
	}

	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	execDB("UPDATE instances SET always_on=1 WHERE id=?", victim.ID)
	if err := s.evictInstance(ctx, victim.ID); err != nil {
		t.Fatalf("always-on eviction-enabled victim should remain eligible: %v", err)
	}
	if state := sup.Status(victim.ID).State; state != supervisor.Unloaded {
		t.Fatalf("always-on victim was not evicted: %s", state)
	}
	if reason := s.resourceBlockReason(victim.ID); reason != resourcePressureReason {
		t.Fatalf("resource block reason=%q", reason)
	}
	if s.isManuallyStopped(victim.ID) {
		t.Fatal("resource-pressure eviction must stay distinct from manual stop")
	}

	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	if reason := s.resourceBlockReason(victim.ID); reason != "" {
		t.Fatalf("explicit start should clear resource block, got %q", reason)
	}
	execDB("UPDATE instances SET eviction_enabled=0 WHERE id=?", victim.ID)
	if err := s.evictInstance(ctx, victim.ID); err == nil || !strings.Contains(err.Error(), "no longer eligible") {
		t.Fatalf("eviction-disabled victim should be protected: %v", err)
	}
}

func TestHardwareSnapshotUsesConfiguredSnapshotter(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, false, false)
	fake := &sequenceHardware{snapshots: []hardware.Snapshot{{RAMAvailableBytes: 1234}}}
	s.hardware = fake
	snapshot, err := s.HardwareSnapshot(context.Background())
	if err != nil || snapshot.RAMAvailableBytes != 1234 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
}
