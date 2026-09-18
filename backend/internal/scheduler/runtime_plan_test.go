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
		Capabilities: RuntimeCapabilities{GPULayers: true},
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
		Capabilities: RuntimeCapabilities{NoKVOffload: true, GPULayers: true},
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
		Capabilities: RuntimeCapabilities{NCPUMoe: true, CPUMoe: true, NoKVOffload: true, GPULayers: true},
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
		Capabilities: RuntimeCapabilities{NoKVOffload: true, GPULayers: true},
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
		Capabilities: RuntimeCapabilities{GPULayers: true},
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


func TestPlanRuntimeNoGPUUsesCPUWithoutRequiringSpillPolicy(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	plan, err := PlanRuntime(RuntimePlanRequest{
		Snapshot: hardware.Snapshot{RAMAvailableBytes: 16 * gib},
		Demand: DemandInput{WeightsBytes: 4 * gib},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fits || plan.Mode != "cpu" || plan.RequiresSpillover || len(plan.Placement.Devices) != 0 {
		t.Fatalf("no-GPU plan=%+v", plan)
	}
}


func TestPlanRuntimeIdleFullFitDoesNotInventMoESpillWhenPolicyOff(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	idle := hardware.Snapshot{
		RAMAvailableBytes: 32 * gib,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 10 * gib}},
	}
	req := RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMAvailableBytes: 32 * gib,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 4 * gib}},
		},
		IdleSnapshot: &idle,
		Demand: DemandInput{
			WeightsBytes: 8 * gib,
			Metadata: KVMetadata{BlockCount: 16, ExpertCount: 64},
		},
		Placement: PlacementRequest{Mode: "auto"},
		Capabilities: RuntimeCapabilities{NCPUMoe: true, CPUMoe: true},
	}
	plan, err := PlanRuntime(req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fits || plan.Mode != "full" {
		t.Fatalf("spillover-off current plan must remain GPU-or-fail when idle hardware fits fully: %+v", plan)
	}
	if _, ok := plan.Options["n-cpu-moe"]; ok {
		t.Fatalf("idle-only pressure must not invent expert spill: %v", plan.Options)
	}
	if _, ok := plan.Options["cpu-moe"]; ok {
		t.Fatalf("idle-only pressure must not invent cpu-moe: %v", plan.Options)
	}
}

func TestPlanRuntimeHonorsExplicitOffloadWithoutSystemSpillPolicy(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	req := RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMTotalBytes: 32 * gib, RAMAvailableBytes: 24 * gib,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 4 * gib}},
		},
		Demand: DemandInput{
			WeightsBytes: 8 * gib,
			Metadata: KVMetadata{BlockCount: 8},
			Options: map[string]string{"n-gpu-layers": "2"},
		},
		Placement: PlacementRequest{Mode: "auto"},
		Capabilities: RuntimeCapabilities{GPULayers: true},
	}
	plan, err := PlanRuntime(req)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fits || plan.Mode != "partial" {
		t.Fatalf("explicit user offload must be evaluated as configured: %+v", plan)
	}
	if plan.RequiresSpillover {
		t.Fatalf("explicit offload is user configuration, not automatic spillover: %+v", plan)
	}
	if plan.Options["n-gpu-layers"] != "2" || plan.Demand.HostRAMBytes <= 0 {
		t.Fatalf("explicit offload options/demand=%+v", plan)
	}
}

func TestPlanRuntimeNoGPUEmitsCPUFlagWhenSupported(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	plan, err := PlanRuntime(RuntimePlanRequest{
		Snapshot: hardware.Snapshot{RAMTotalBytes: 16 * gib, RAMAvailableBytes: 12 * gib},
		Demand: DemandInput{WeightsBytes: 4 * gib, Options: map[string]string{"ctx-size": "4096"}},
		Capabilities: RuntimeCapabilities{GPULayers: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fits || plan.Mode != "cpu" || plan.Options["n-gpu-layers"] != "0" {
		t.Fatalf("CPU-only host must force zero GPU layers when supported: %+v", plan)
	}
}

func TestRuntimeHostRAMHeadroomBoundaries(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	if !runtimeHostRAMFits(hardware.Snapshot{}, 8*gib) {
		t.Fatal("unknown RAM telemetry must preserve compatibility")
	}
	if !runtimeHostRAMFits(hardware.Snapshot{RAMAvailableBytes: gib}, 0) {
		t.Fatal("zero host demand must fit")
	}
	if runtimeHostRAMFits(hardware.Snapshot{RAMAvailableBytes: gib}, 1) {
		t.Fatal("the 1 GiB reserve must be preserved")
	}
	if runtimeHostRAMFits(hardware.Snapshot{RAMAvailableBytes: 5 * gib}, 4*gib+1) {
		t.Fatal("demand above available-minus-reserve must not fit")
	}
	if !runtimeHostRAMFits(hardware.Snapshot{RAMAvailableBytes: 5 * gib}, 4*gib) {
		t.Fatal("exact available-minus-reserve boundary should fit")
	}
}

func TestPlanAutomaticMoERequiresBlockMetadata(t *testing.T) {
	plan, ok, err := planAutomaticMoE(RuntimePlanRequest{
		Snapshot: hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 << 30}}},
		Demand: DemandInput{WeightsBytes: 4 << 30},
		Placement: PlacementRequest{Mode: "auto"},
	}, nil)
	if err != nil || ok || plan.Fits {
		t.Fatalf("metadata-free MoE plan=%+v ok=%v err=%v", plan, ok, err)
	}
}

func TestHasExplicitOffloadRecognizesCanonicalAndCLIKeys(t *testing.T) {
	for _, options := range []map[string]string{
		{"gpu-layers": "2"},
		{"--n-gpu-layers": "2"},
		{"cpu-moe": "true"},
		{"--n-cpu-moe": "4"},
		{"no-kv-offload": "true"},
	} {
		if !hasExplicitOffload(options) {
			t.Fatalf("explicit offload not recognized: %v", options)
		}
	}
	if hasExplicitOffload(map[string]string{"ctx-size": "4096"}) || hasExplicitOffload(nil) {
		t.Fatal("ordinary launch options must not count as explicit offload")
	}
}
