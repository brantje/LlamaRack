package scheduler

import (
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
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

func TestPlanPlacementRejectsInvalidRequests(t *testing.T) {
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 10 << 30}}}
	if _, err := PlanPlacement(snapshot, PlacementRequest{RequiredBytes: -1}); err == nil || !strings.Contains(err.Error(), "zero or greater") {
		t.Fatalf("negative requirement should fail: %v", err)
	}
	if _, err := PlanPlacement(snapshot, PlacementRequest{Mode: "sideways"}); err == nil || !strings.Contains(err.Error(), "auto or manual") {
		t.Fatalf("invalid mode should fail: %v", err)
	}
	if _, err := PlanPlacement(snapshot, PlacementRequest{Mode: "manual"}); err == nil || !strings.Contains(err.Error(), "at least one device") {
		t.Fatalf("empty manual placement should fail: %v", err)
	}
}

func TestPlanPlacementNoGPUAndZeroRequirement(t *testing.T) {
	placement, err := PlanPlacement(hardware.Snapshot{}, PlacementRequest{})
	if err != nil || !placement.Fits || placement.RequiredBytes != 0 {
		t.Fatalf("zero-byte CPU-only placement=%+v err=%v", placement, err)
	}
	placement, err = PlanPlacement(hardware.Snapshot{}, PlacementRequest{RequiredBytes: 1})
	if err != nil || placement.Fits {
		t.Fatalf("positive CPU-only requirement=%+v err=%v", placement, err)
	}
}

func TestPlanPlacementManualDeduplicatesAndCanRemainInsufficient(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 2 * gib}}}
	placement, err := PlanPlacement(snapshot, PlacementRequest{
		RequiredBytes: 3 * gib,
		Mode:          "manual",
		Devices:       []string{" CUDA0 ", "CUDA0"},
		ReserveBytes:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if placement.Fits || len(placement.Devices) != 1 {
		t.Fatalf("expected one deduplicated insufficient device, got %+v", placement)
	}
}

func TestPlanPlacementSkipsDevicesConsumedByReserveAndSortsTies(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA2", FreeBytes: 256 * 1024 * 1024},
		{ID: "CUDA1", FreeBytes: 2 * gib},
		{ID: "CUDA0", FreeBytes: 2 * gib},
	}}
	placement, err := PlanPlacement(snapshot, PlacementRequest{RequiredBytes: 4 * gib})
	if err != nil {
		t.Fatal(err)
	}
	if placement.Fits || len(placement.Devices) != 2 || placement.Devices[0] != "CUDA0" || placement.Devices[1] != "CUDA1" {
		t.Fatalf("expected tied usable GPUs sorted by ID and reserved GPU skipped, got %+v", placement)
	}
}

func TestPlacementFormattingHelpers(t *testing.T) {
	if got := usableVRAM(hardware.GPU{FreeBytes: 100}, 200); got != 0 {
		t.Fatalf("reserved VRAM should floor at zero, got %d", got)
	}
	if got := tensorSplitFor([]int64{1, 512 * 1024 * 1024}); got != "1,2" {
		t.Fatalf("unexpected split: %q", got)
	}
	if got := intString(0); got != "0" {
		t.Fatalf("zero formatting=%q", got)
	}
	if got := intString(-42); got != "-42" {
		t.Fatalf("negative formatting=%q", got)
	}
}
