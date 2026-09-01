package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
)

type stagedHardware struct {
	snapshots []hardware.Snapshot
	errors    []error
	calls     int
}

func (s *stagedHardware) Snapshot(context.Context) (hardware.Snapshot, error) {
	index := s.calls
	s.calls++
	if index < len(s.errors) && s.errors[index] != nil {
		return hardware.Snapshot{}, s.errors[index]
	}
	if len(s.snapshots) == 0 {
		return hardware.Snapshot{}, nil
	}
	if index >= len(s.snapshots) {
		index = len(s.snapshots) - 1
	}
	return s.snapshots[index], nil
}

func TestPreparePlacementReportsRefreshFailureAfterEviction(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	victim := items[0]
	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 2*testGiB, m.ID)

	s.hardware = &stagedHardware{
		snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: testGiB}}}},
		errors:    []error{nil, errors.New("refresh failed")},
	}
	_, err = s.preparePlacement(ctx, instances.Instance{ID: "target", GPUMode: "auto"}, 2*testGiB)
	if err == nil || !strings.Contains(err.Error(), "refresh hardware after eviction") {
		t.Fatalf("expected refresh failure, got %v", err)
	}
}

func TestPreparePlacementRejectsCapacityThatDidNotRecover(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	victim := items[0]
	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 2*testGiB, m.ID)

	s.hardware = &stagedHardware{snapshots: []hardware.Snapshot{
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: testGiB}}},
		{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: testGiB}}},
	}}
	_, err = s.preparePlacement(ctx, instances.Instance{ID: "target", GPUMode: "auto"}, 2*testGiB)
	if err == nil || !strings.Contains(err.Error(), "after eviction") {
		t.Fatalf("expected post-eviction capacity failure, got %v", err)
	}
}

func TestStartOneWithEvictionPropagatesMissingModel(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	_, err := s.startOneWithEviction(context.Background(), instances.Instance{ID: "orphan", ModelID: "missing-model"}, false)
	if err == nil {
		t.Fatal("expected missing Model error")
	}
}

func TestEvictionPlanPropagatesDatabaseFailure(t *testing.T) {
	s, _, _, _, _ := setupLifecycle(t, true, false)
	if err := s.models.DB().Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EvictionPlan(context.Background(), testGiB); err == nil {
		t.Fatal("expected closed database error")
	}
}
