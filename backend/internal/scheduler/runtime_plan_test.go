package scheduler

import (
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

func TestPlanRuntimeSystemSpilloverPolicy(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	req := RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMAvailableBytes: 64 * gib,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}},
		},
		Demand: DemandInput{
			WeightsBytes: 10 * gib,
			Metadata: KVMetadata{BlockCount: 10},
			Options: map[string]string{"ctx-size": "4096"},
		},
		Placement: PlacementRequest{Mode: "auto"},
	}
	without, err := PlanRuntime(req)
	if err != nil {
		t.Fatal(err)
	}
	if without.Fits {
		t.Fatalf("spillover disabled must preserve GPU-or-fail behavior: %+v", without)
	}

	req.AllowSystemSpillover = true
	with, err := PlanRuntime(req)
	if err != nil {
		t.Fatal(err)
	}
	if !with.Fits || with.Mode != "partial" || !with.RequiresSpillover {
		t.Fatalf("spillover plan=%+v", with)
	}
	if with.Demand.HostRAMBytes <= 0 || with.Demand.VRAMBytes() <= 0 || with.Options["n-gpu-layers"] == "" {
		t.Fatalf("partial spill must reserve GPU and host RAM: %+v", with)
	}
}

func TestPlanRuntimeMovesKVToRAMWhenKVAloneExceedsVRAM(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	req := RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMAvailableBytes: 64 * gib,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}},
		},
		Demand: DemandInput{
			WeightsBytes: 5 * gib,
			Context: 262144,
			Metadata: KVMetadata{BlockCount: 10, Embedding: 1024, HeadCount: 8, KVHeadCount: 8},
		},
		Placement: PlacementRequest{Mode: "auto"},
		AllowSystemSpillover: true,
		Capabilities: RuntimeCapabilities{NoKVOffload: true},
	}
	plan, err := PlanRuntime(req)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fits || plan.Mode != "hybrid" || plan.Options["no-kv-offload"] != "true" {
		t.Fatalf("hybrid plan=%+v", plan)
	}
	if plan.Demand.KVCacheBytes <= 8*gib || plan.Demand.HostRAMBytes < plan.Demand.KVCacheBytes {
		t.Fatalf("expected KV cliff to move into RAM: %+v", plan.Demand)
	}
}

func TestPlanRuntimeMoEUsesExactDemandAndCompanions(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	req := RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMAvailableBytes: 64 * gib,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}, {ID: "CUDA1", FreeBytes: 8 * gib}},
		},
		Demand: DemandInput{
			WeightsBytes: 20 * gib, CompanionBytes: gib, Context: 4096,
			Metadata: KVMetadata{BlockCount: 40, Embedding: 4096, HeadCount: 32, KVHeadCount: 8, ExpertCount: 64},
		},
		Placement: PlacementRequest{Mode: "auto"},
		Capabilities: RuntimeCapabilities{NCPUMoe: true, CPUMoe: true, NoKVOffload: true},
	}
	plan, err := PlanRuntime(req)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fits || plan.Mode != "moe" {
		t.Fatalf("moe plan=%+v", plan)
	}
	if plan.Demand.WeightsBytes != 21*gib {
		t.Fatalf("weights=%d want companions included", plan.Demand.WeightsBytes)
	}
	if plan.Options["n-cpu-moe"] == "" && plan.Options["cpu-moe"] != "true" {
		t.Fatalf("missing expert spill option: %v", plan.Options)
	}
}

func TestPlanRuntimeManualSpillNeverSubstitutesAnotherGPU(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	plan, err := PlanRuntime(RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMAvailableBytes: 64 * gib,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 4 * gib}, {ID: "CUDA1", FreeBytes: 16 * gib}},
		},
		Demand: DemandInput{WeightsBytes: 6 * gib, Metadata: KVMetadata{BlockCount: 12}},
		Placement: PlacementRequest{Mode: "manual", Devices: []string{"CUDA0"}},
		AllowSystemSpillover: true,
		Capabilities: RuntimeCapabilities{NoKVOffload: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fits {
		t.Fatalf("manual spill should fit via configured GPU or CPU: %+v", plan)
	}
	for _, device := range plan.Placement.Devices {
		if device != "CUDA0" {
			t.Fatalf("manual spill substituted %s: %+v", device, plan)
		}
	}
}

func TestPlanRuntimeCPUFallbackNeedsRAMHeadroom(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	req := RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMAvailableBytes: 24 * gib,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 4 * gib}},
		},
		Demand: DemandInput{WeightsBytes: 20 * gib, Metadata: KVMetadata{BlockCount: 0}},
		Placement: PlacementRequest{Mode: "auto"},
		AllowSystemSpillover: true,
	}
	plan, err := PlanRuntime(req)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fits || plan.Mode != "cpu" || len(plan.Placement.Devices) != 0 {
		t.Fatalf("cpu plan=%+v", plan)
	}
	req.Snapshot.RAMAvailableBytes = 21 * gib
	plan, err = PlanRuntime(req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fits {
		t.Fatalf("1 GiB RAM reserve must make this no-fit: %+v", plan)
	}
}
