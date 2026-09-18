package recommendations

import (
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

func TestRuntimePlacementRangesIncludeCompanionsInRunnableCeiling(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	meta := Metadata{
		ContextLength: 131072,
		BlockCount: 32, Embedding: 4096, HeadCount: 32, KVHeadCount: 8,
	}
	snapshot := hardware.Snapshot{
		RAMTotalBytes: 64 * gib, RAMAvailableBytes: 64 * gib,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 16 * gib, TotalBytes: 16 * gib}},
	}
	base := ComputeRuntimePlacementRanges(snapshot, 8*gib, 0, meta, nil, meta.ContextLength, Capabilities{}, RuntimeConfig{GPUMode: "auto"})
	withCompanion := ComputeRuntimePlacementRanges(snapshot, 8*gib, gib, meta, nil, meta.ContextLength, Capabilities{}, RuntimeConfig{GPUMode: "auto"})
	if !base.Available || !withCompanion.Available {
		t.Fatalf("ranges base=%+v companion=%+v", base, withCompanion)
	}
	if withCompanion.MaximumContext >= base.MaximumContext {
		t.Fatalf("companion must lower runnable context: base=%d companion=%d", base.MaximumContext, withCompanion.MaximumContext)
	}
	if withCompanion.MaximumContext >= meta.ContextLength {
		t.Fatalf("runnable maximum %d must remain separate from model capability %d", withCompanion.MaximumContext, meta.ContextLength)
	}
}

func TestRuntimePlacementRangesFollowSpilloverPolicy(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	meta := Metadata{ContextLength: 32768, BlockCount: 10, Embedding: 1024, HeadCount: 8, KVHeadCount: 8}
	snapshot := hardware.Snapshot{
		RAMTotalBytes: 64 * gib, RAMAvailableBytes: 64 * gib,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}},
	}
	off := ComputeRuntimePlacementRanges(snapshot, 10*gib, 0, meta, nil, meta.ContextLength, Capabilities{GPULayers: true, NoKVOffload: true}, RuntimeConfig{GPUMode: "auto"})
	if off.Available {
		t.Fatalf("spillover-off range must not advertise unrunnable partial placement: %+v", off)
	}
	on := ComputeRuntimePlacementRanges(snapshot, 10*gib, 0, meta, nil, meta.ContextLength, Capabilities{GPULayers: true, NoKVOffload: true}, RuntimeConfig{GPUMode: "auto", AllowSystemSpillover: true})
	if !on.Available || on.MaximumContext == 0 || len(on.Zones) == 0 {
		t.Fatalf("spillover-on range=%+v", on)
	}
	if on.Zones[0].OffloadMode != "partial" && on.Zones[0].OffloadMode != "hybrid" && on.Zones[0].OffloadMode != "cpu" {
		t.Fatalf("unexpected spill zone=%+v", on.Zones[0])
	}
}

func TestRuntimePlacementRangeClassificationMatchesSchedulerPlan(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	meta := Metadata{
		ContextLength: 65536, BlockCount: 24, Embedding: 2048, HeadCount: 16, KVHeadCount: 8,
		ExpertCount: 64,
	}
	snapshot := hardware.Snapshot{
		RAMTotalBytes: 96 * gib, RAMAvailableBytes: 72 * gib,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 10 * gib}, {ID: "CUDA1", FreeBytes: 9 * gib}},
	}
	caps := Capabilities{NCPUMoe: true, CPUMoe: true, NoKVOffload: true, GPULayers: true}
	runtime := RuntimeConfig{GPUMode: "auto", AllowSystemSpillover: true}
	ranges := ComputeRuntimePlacementRanges(snapshot, 20*gib, gib, meta, nil, meta.ContextLength, caps, runtime)
	if !ranges.Available {
		t.Fatalf("ranges=%+v", ranges)
	}
	for _, zone := range ranges.Zones {
		for _, context := range []int64{zone.StartContext, zone.EndContext} {
			classified := classifyRuntime(snapshot, nil, 20*gib, gib, context, meta, nil, caps, runtime)
			if !classified.Fit {
				t.Fatalf("advertised context %d does not fit: zone=%+v", context, zone)
			}
			kind, count := placementKind(classified)
			if kind != zone.Kind || count != zone.GPUCount {
				t.Fatalf("context %d classified=%s:%d zone=%+v", context, kind, count, zone)
			}
		}
	}
	next := ranges.MaximumContext + ranges.ContextStep
	if next <= meta.ContextLength {
		classified := classifyRuntime(snapshot, nil, 20*gib, gib, next, meta, nil, caps, runtime)
		if classified.Fit {
			t.Fatalf("context after advertised maximum still fits: max=%d next=%d", ranges.MaximumContext, next)
		}
	}
}
