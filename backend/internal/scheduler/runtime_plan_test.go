package scheduler

import (
	"strings"
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
	if runtimeHostRAMFits(hardware.Snapshot{}, 8*gib) {
		t.Fatal("unknown RAM telemetry cannot prove a positive host-RAM demand fits")
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
	}, nil, runtimeOptionLocks{})
	if err != nil || ok || plan.Fits {
		t.Fatalf("metadata-free MoE plan=%+v ok=%v err=%v", plan, ok, err)
	}
}

func TestRuntimeOptionLocksArePerDimension(t *testing.T) {
	locks := runtimeOptionLocksFor(map[string]string{
		"--gpu-layers": "2",
		"cpu-moe": "false",
		"no-kv-offload": "false",
	})
	if !locks.GPULayers || !locks.MoE || !locks.KV {
		t.Fatalf("locks=%+v", locks)
	}
	if locks := runtimeOptionLocksFor(map[string]string{"ctx-size": "4096"}); locks != (runtimeOptionLocks{}) {
		t.Fatalf("ordinary options locked planner dimensions: %+v", locks)
	}
}

func TestPlanRuntimeExplicitLayersCanStillMoveKV(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	plan, err := PlanRuntime(RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMTotalBytes: 64 * gib, RAMAvailableBytes: 48 * gib,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 5 * gib}},
		},
		Demand: DemandInput{
			WeightsBytes: 8 * gib, Context: 32768,
			Metadata: KVMetadata{BlockCount: 8, Embedding: 4096, HeadCount: 32, KVHeadCount: 32},
			Options: map[string]string{"n-gpu-layers": "4"},
		},
		Placement: PlacementRequest{Mode: "auto"},
		AllowSystemSpillover: true,
		Capabilities: RuntimeCapabilities{GPULayers: true, GPULayersOption: "n-gpu-layers", NoKVOffload: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fits || plan.Options["n-gpu-layers"] != "4" || !optionEnabled(plan.Options, "no-kv-offload") {
		t.Fatalf("explicit layers must stay locked while KV may move: %+v", plan)
	}
}

func TestPlanRuntimeExplicitKVCanStillPlanLayers(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	plan, err := PlanRuntime(RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMTotalBytes: 64 * gib, RAMAvailableBytes: 48 * gib,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 5 * gib}},
		},
		Demand: DemandInput{
			WeightsBytes: 8 * gib,
			Metadata: KVMetadata{BlockCount: 8},
			Options: map[string]string{"no-kv-offload": "false"},
		},
		Placement: PlacementRequest{Mode: "auto"},
		AllowSystemSpillover: true,
		Capabilities: RuntimeCapabilities{GPULayers: true, GPULayersOption: "n-gpu-layers", NoKVOffload: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fits || optionEnabled(plan.Options, "no-kv-offload") || strings.TrimSpace(plan.Options["n-gpu-layers"]) == "" {
		t.Fatalf("explicit KV-on-GPU policy must stay locked while layers may spill: %+v", plan)
	}
}

func TestPlanRuntimeUsesAdvertisedGPULayerAlias(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	plan, err := PlanRuntime(RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMTotalBytes: 64 * gib, RAMAvailableBytes: 48 * gib,
			GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 5 * gib}},
		},
		Demand: DemandInput{WeightsBytes: 8 * gib, Metadata: KVMetadata{BlockCount: 8}},
		Placement: PlacementRequest{Mode: "auto"},
		AllowSystemSpillover: true,
		Capabilities: RuntimeCapabilities{GPULayers: true, GPULayersOption: "gpu-layers"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Fits || strings.TrimSpace(plan.Options["gpu-layers"]) == "" {
		t.Fatalf("alias-only runtime plan=%+v", plan)
	}
	if _, wrong := plan.Options["n-gpu-layers"]; wrong {
		t.Fatalf("planner generated unsupported alias: %v", plan.Options)
	}
}

func TestPlanRuntimeRejectsSpillWhenGPUKnownButHostRAMUnknown(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	plan, err := PlanRuntime(RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 4 * gib, FreeBytes: 4 * gib}},
		},
		Demand: DemandInput{
			WeightsBytes: 6 * gib,
			Metadata: KVMetadata{BlockCount: 12},
		},
		Placement: PlacementRequest{Mode: "auto"},
		AllowSystemSpillover: true,
		Capabilities: RuntimeCapabilities{GPULayers: true, NoKVOffload: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Fits {
		t.Fatalf("host-RAM spill must not fit with unknown RAM telemetry: %+v", plan)
	}
}


// Regression for #226: the GPU-fit predicate is monotone, but its
// conjunction with host-RAM fit is not. The valid layer interval is 52..62.
func TestPlanRuntimeDenseHybridFindsFeasibleBand(t *testing.T) {
	req := denseHybridRegressionRequest()
	for _, tc := range []struct {
		layers  int64
		gpuFits bool
		ramFits bool
	}{
		{33, true, false},
		{62, true, true},
		{63, false, true},
	} {
		options := map[string]string{
			"n-gpu-layers":  intString(tc.layers),
			"no-kv-offload": "true",
		}
		demand := demandFor(req.Demand, options)
		placement, err := planDemandPlacement(req.Placement, req.Snapshot, demand)
		if err != nil {
			t.Fatal(err)
		}
		if placement.Fits != tc.gpuFits || runtimeHostRAMFits(req.Snapshot, demand.HostRAMBytes) != tc.ramFits {
			t.Fatalf("layers=%d: GPU fits=%v, RAM fits=%v; demand=%+v", tc.layers, placement.Fits, runtimeHostRAMFits(req.Snapshot, demand.HostRAMBytes), demand)
		}
	}
	for _, context := range []int64{36864, 44544} {
		req.Demand.Context = context
		plan, err := PlanRuntime(req)
		if err != nil {
			t.Fatal(err)
		}
		if !plan.Fits || plan.Mode != "hybrid" || !plan.RequiresSpillover || plan.Options["n-gpu-layers"] != "62" || plan.Options["no-kv-offload"] != "true" {
			t.Fatalf("ctx=%d: expected feasible 62-layer hybrid, got %+v", context, plan)
		}
		if !plan.Placement.Fits || !runtimeHostRAMFits(req.Snapshot, plan.Demand.HostRAMBytes) {
			t.Fatalf("ctx=%d: selected plan exceeds available resources: %+v", context, plan)
		}
	}
}

func denseHybridRegressionRequest() RuntimePlanRequest {
	return RuntimePlanRequest{
		Snapshot: hardware.Snapshot{
			RAMTotalBytes:     16106127360,
			RAMAvailableBytes: 15032385536,
			GPUs:              []hardware.GPU{{ID: "CUDA0", TotalBytes: 12884901888, FreeBytes: 12883906560}},
		},
		Demand: DemandInput{
			WeightsBytes:   9828981664,
			CompanionBytes: 927607488 + 1369590656,
			Context:        44544,
			Metadata: KVMetadata{
				BlockCount: 65, Embedding: 8192, HeadCount: 32, KVHeadCount: 4,
			},
		},
		Placement:            PlacementRequest{Mode: "auto"},
		AllowSystemSpillover: true,
		Capabilities:         RuntimeCapabilities{GPULayers: true, NoKVOffload: true},
	}
}

// Compare the logarithmic search with a test-only exhaustive oracle.
// This also covers manual-device constraints and both KV placements.
func TestPlanDensePartialMatchesExhaustiveFeasibleSearch(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	scenarios := []struct {
		name         string
		context      int64
		ramAvailable int64
		GPUs         []hardware.GPU
		placement    PlacementRequest
	}{
		{"bounded-fit", 44544, 15032385536, []hardware.GPU{{ID: "CUDA0", FreeBytes: 12883906560}}, PlacementRequest{Mode: "auto"}},
		{"lower-context", 34816, 15032385536, []hardware.GPU{{ID: "CUDA0", FreeBytes: 12883906560}}, PlacementRequest{Mode: "auto"}},
		{"tight-host-ram", 44544, 8 * gib, []hardware.GPU{{ID: "CUDA0", FreeBytes: 12883906560}}, PlacementRequest{Mode: "auto"}},
		{"unknown-host-ram", 44544, 0, []hardware.GPU{{ID: "CUDA0", FreeBytes: 12883906560}}, PlacementRequest{Mode: "auto"}},
		{"tight-vram", 44544, 32 * gib, []hardware.GPU{{ID: "CUDA0", FreeBytes: 4 * gib}}, PlacementRequest{Mode: "auto"}},
		{"ample-resources", 4096, 32 * gib, []hardware.GPU{{ID: "CUDA0", FreeBytes: 24 * gib}}, PlacementRequest{Mode: "auto"}},
		{"multi-gpu-auto", 44544, 32 * gib, []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}, {ID: "CUDA1", FreeBytes: 8 * gib}}, PlacementRequest{Mode: "auto"}},
		{"manual-single", 44544, 15032385536, []hardware.GPU{{ID: "CUDA0", FreeBytes: 12883906560}, {ID: "CUDA1", FreeBytes: 24 * gib}}, PlacementRequest{Mode: "manual", Devices: []string{"CUDA0"}}},
		{"manual-split", 44544, 32 * gib, []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}, {ID: "CUDA1", FreeBytes: 8 * gib}}, PlacementRequest{Mode: "manual", Devices: []string{"CUDA0", "CUDA1"}, TensorSplit: "1,1"}},
	}
	for _, tc := range scenarios {
		for _, moveKV := range []bool{false, true} {
			req := denseHybridRegressionRequest()
			req.Demand.Context = tc.context
			req.Snapshot.GPUs = tc.GPUs
			req.Snapshot.RAMAvailableBytes = tc.ramAvailable
			req.Placement = tc.placement
			name := tc.name + "/kv-on-gpu"
			if moveKV {
				name = tc.name + "/kv-in-ram"
			}
			t.Run(name, func(t *testing.T) {
				best := int64(0)
				for layer := int64(1); layer <= req.Demand.Metadata.BlockCount; layer++ {
					options := map[string]string{"n-gpu-layers": intString(layer)}
					if moveKV {
						options["no-kv-offload"] = "true"
					}
					demand := demandFor(req.Demand, options)
					placement, err := planDemandPlacement(req.Placement, req.Snapshot, demand)
					if err != nil {
						t.Fatal(err)
					}
					if placement.Fits && runtimeHostRAMFits(req.Snapshot, demand.HostRAMBytes) {
						best = layer
					}
				}
				plan, ok, err := planDensePartial(req, nil, moveKV)
				if err != nil {
					t.Fatal(err)
				}
				if ok != (best > 0) {
					t.Fatalf("planner found=%v, exhaustive best=%d, plan=%+v", ok, best, plan)
				}
				if !ok {
					return
				}
				if got := plan.Options["n-gpu-layers"]; got != intString(best) {
					t.Fatalf("picked GPU layers=%q, exhaustive best=%d", got, best)
				}
				if !plan.Fits || !plan.RequiresSpillover || !plan.Placement.Fits || !runtimeHostRAMFits(req.Snapshot, plan.Demand.HostRAMBytes) {
					t.Fatalf("infeasible candidate selected: %+v", plan)
				}
			})
		}
	}
}
