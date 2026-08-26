package scheduler

import (
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
)

func TestPlanPlacementPrefersOneGPU(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 20 * gib},
		{ID: "CUDA1", FreeBytes: 16 * gib},
	}}
	placement, err := PlanPlacement(snapshot, PlacementRequest{RequiredBytes: 12 * gib})
	if err != nil {
		t.Fatal(err)
	}
	if !placement.Fits || len(placement.Devices) != 1 || placement.Devices[0] != "CUDA0" {
		t.Fatalf("expected CUDA0-only placement, got %+v", placement)
	}
	if placement.TensorSplit != "" {
		t.Fatalf("single-GPU placement must not generate tensor split, got %q", placement.TensorSplit)
	}
}

func TestPlanPlacementUsesMultipleGPUsOnlyWhenRequired(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 10 * gib},
		{ID: "CUDA1", FreeBytes: 9 * gib},
	}}
	placement, err := PlanPlacement(snapshot, PlacementRequest{RequiredBytes: 14 * gib})
	if err != nil {
		t.Fatal(err)
	}
	if !placement.Fits || len(placement.Devices) != 2 {
		t.Fatalf("expected two-GPU placement, got %+v", placement)
	}
	if placement.TensorSplit == "" {
		t.Fatal("expected automatic tensor split for multi-GPU placement")
	}
}

func TestPlanPlacementManualHonorsConfiguredDevices(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 24 * gib},
		{ID: "CUDA1", FreeBytes: 24 * gib},
	}}
	placement, err := PlanPlacement(snapshot, PlacementRequest{RequiredBytes: 8 * gib, Mode: "manual", Devices: []string{"CUDA1"}, TensorSplit: "3,1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(placement.Devices) != 1 || placement.Devices[0] != "CUDA1" {
		t.Fatalf("manual placement changed devices: %+v", placement)
	}
	if placement.TensorSplit != "3,1" {
		t.Fatalf("manual tensor split changed: %q", placement.TensorSplit)
	}
}

func TestPlanPlacementRejectsMissingManualDevice(t *testing.T) {
	_, err := PlanPlacement(hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 10 << 30}}}, PlacementRequest{Mode: "manual", Devices: []string{"CUDA9"}})
	if err == nil {
		t.Fatal("expected missing manual device to fail")
	}
}
