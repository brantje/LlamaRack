package recommendations

import (
	"fmt"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/models"
)

func rangeMeta() Metadata {
	return Metadata{
		Architecture: "qwen2", ContextLength: 262144, BlockCount: 32, Embedding: 4096,
		HeadCount: 32, KVHeadCount: 8,
	}
}

func gpu(id string, free, total int64) hardware.GPU {
	return hardware.GPU{ID: id, Name: id, FreeBytes: free, TotalBytes: total}
}

func TestComputePlacementRangesSingleGPU(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{RAMAvailableBytes: 64 * gib, RAMTotalBytes: 64 * gib, GPUs: []hardware.GPU{gpu("CUDA0", 24*gib, 24*gib)}}
	ranges := ComputePlacementRanges(snapshot, 4*gib, rangeMeta(), 8192)
	assertRangeInvariants(t, snapshot, 4*gib, rangeMeta(), ranges)
	if len(ranges.Zones) != 1 || ranges.Zones[0].Kind != "gpu" || ranges.Zones[0].GPUCount != 1 || ranges.Zones[0].OffloadMode != "full" {
		t.Fatalf("single gpu=%+v", ranges.Zones)
	}
	if ranges.GPUOnlyMaxContext != 8192 || !ranges.Zones[0].CurrentFit || !ranges.Zones[0].TotalHardwareFit {
		t.Fatalf("gpu-only max=%+v", ranges)
	}
}

func TestComputePlacementRangesOneToTwoGPUs(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 64 * gib, RAMTotalBytes: 64 * gib,
		GPUs: []hardware.GPU{gpu("CUDA0", 8*gib, 8*gib), gpu("CUDA1", 8*gib, 8*gib)},
	}
	ranges := ComputePlacementRanges(snapshot, 6*gib, rangeMeta(), 32768)
	assertRangeInvariants(t, snapshot, 6*gib, rangeMeta(), ranges)
	assertKindSequence(t, ranges, "gpu:1", "gpu:2")
	if ranges.Zones[0].Devices[0] != "CUDA0" && ranges.Zones[0].Devices[0] != "CUDA1" {
		t.Fatalf("single device=%v", ranges.Zones[0].Devices)
	}
	if len(ranges.Zones[1].Devices) != 2 || ranges.Zones[1].TensorSplit == "" {
		t.Fatalf("two gpu zone=%+v", ranges.Zones[1])
	}
}

func TestComputePlacementRangesTwoToThreeGPUs(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 128 * gib, RAMTotalBytes: 128 * gib,
		GPUs: []hardware.GPU{gpu("CUDA0", 7*gib, 7*gib), gpu("CUDA1", 7*gib, 7*gib), gpu("CUDA2", 7*gib, 7*gib)},
	}
	ranges := ComputePlacementRanges(snapshot, 8*gib, rangeMeta(), 65536)
	assertRangeInvariants(t, snapshot, 8*gib, rangeMeta(), ranges)
	assertKindSequenceContains(t, ranges, "gpu:2", "gpu:3")
}

func TestComputePlacementRangesNGPUAndHybrid(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 256 * gib, RAMTotalBytes: 256 * gib,
		GPUs: []hardware.GPU{
			gpu("CUDA0", 8*gib, 8*gib), gpu("CUDA1", 8*gib, 8*gib),
			gpu("CUDA2", 8*gib, 8*gib), gpu("CUDA3", 8*gib, 8*gib),
		},
	}
	ranges := ComputePlacementRanges(snapshot, 10*gib, rangeMeta(), 262144)
	assertRangeInvariants(t, snapshot, 10*gib, rangeMeta(), ranges)
	assertKindSequenceContains(t, ranges, "gpu:2", "gpu:3", "gpu:4")
	gpuMax := ranges.GPUOnlyMaxContext
	if gpuMax < placementMinContext {
		t.Fatalf("missing gpu-only max %+v", ranges)
	}
	hybridStart := gpuMax + placementContextStep
	if hybridStart > ranges.MaximumContext {
		t.Fatalf("no hybrid step after gpu max=%d", gpuMax)
	}
	hybrid := classifyOffload(snapshot, 10*gib, hybridStart, rangeMeta())
	kind, _ := placementKind(hybrid)
	if kind != "hybrid" && kind != "partial" && kind != "cpu" && kind != "no_fit" {
		t.Fatalf("expected post-gpu kind, got %s mode=%s", kind, hybrid.Offload.Mode)
	}
	full := classifyOffload(snapshot, 10*gib, gpuMax, rangeMeta())
	fullKind, _ := placementKind(full)
	if fullKind != "gpu" {
		t.Fatalf("gpu max %d classified as %s", gpuMax, fullKind)
	}
}

func TestComputePlacementRangesHeterogeneousDevices(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 128 * gib, RAMTotalBytes: 128 * gib,
		GPUs: []hardware.GPU{
			gpu("CUDA0", 8*gib, 8*gib),   // smaller, lower index
			gpu("CUDA1", 16*gib, 16*gib), // largest
			gpu("CUDA2", 12*gib, 12*gib),
		},
	}
	ranges := ComputePlacementRanges(snapshot, 10*gib, rangeMeta(), 65536)
	assertRangeInvariants(t, snapshot, 10*gib, rangeMeta(), ranges)
	if ranges.Zones[0].Kind != "gpu" || ranges.Zones[0].GPUCount != 1 || ranges.Zones[0].Devices[0] != "CUDA1" {
		t.Fatalf("expected largest GPU first, got %+v", ranges.Zones[0])
	}
	foundTwo := false
	for _, zone := range ranges.Zones {
		if zone.Kind == "gpu" && zone.GPUCount == 2 {
			foundTwo = true
			if zone.Devices[0] != "CUDA1" || zone.Devices[1] != "CUDA2" {
				t.Fatalf("expected 4090-style prefix CUDA1,CUDA2 got %v", zone.Devices)
			}
		}
	}
	if !foundTwo {
		t.Fatalf("missing 2-GPU zone %+v", ranges.Zones)
	}
}

func TestComputePlacementRangesPartialHybridCPUAndNoFit(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	partialSnap := hardware.Snapshot{
		RAMAvailableBytes: 32 * gib, RAMTotalBytes: 32 * gib,
		GPUs: []hardware.GPU{gpu("CUDA0", 3*gib, 8*gib)},
	}
	partial := ComputePlacementRanges(partialSnap, 4*gib, rangeMeta(), 8192)
	assertRangeInvariants(t, partialSnap, 4*gib, rangeMeta(), partial)
	if partial.Zones[0].Kind != "partial" || !partial.Zones[0].KVOnGPU || !partial.Zones[0].TotalHardwareFit {
		t.Fatalf("partial=%+v", partial.Zones[0])
	}

	hybridSnap := hardware.Snapshot{
		RAMAvailableBytes: 32 * gib, RAMTotalBytes: 32 * gib,
		GPUs: []hardware.GPU{gpu("CUDA0", 8*gib, 8*gib)},
	}
	hybrid := ComputePlacementRanges(hybridSnap, 4*gib, rangeMeta(), 131072)
	assertRangeInvariants(t, hybridSnap, 4*gib, rangeMeta(), hybrid)
	assertKindSequenceContains(t, hybrid, "gpu:1", "hybrid:1")
	var hybridZone PlacementZone
	for _, zone := range hybrid.Zones {
		if zone.Kind == "hybrid" {
			hybridZone = zone
			break
		}
	}
	if hybridZone.KVOnGPU || hybridZone.OffloadMode != "hybrid" {
		t.Fatalf("hybrid zone=%+v", hybridZone)
	}
	gpuEnd := hybrid.GPUOnlyMaxContext
	if classifyOffload(hybridSnap, 4*gib, gpuEnd, rangeMeta()).Offload.Mode == "hybrid" {
		t.Fatalf("gpu-only max %d is already hybrid", gpuEnd)
	}
	next := classifyOffload(hybridSnap, 4*gib, gpuEnd+placementContextStep, rangeMeta())
	nextKind, _ := placementKind(next)
	if nextKind == "gpu" {
		t.Fatalf("next step %d still gpu: %+v", gpuEnd+placementContextStep, next)
	}

	cpuSnap := hardware.Snapshot{RAMAvailableBytes: 16 * gib, RAMTotalBytes: 16 * gib}
	cpu := ComputePlacementRanges(cpuSnap, 4*gib, rangeMeta(), 8192)
	assertRangeInvariants(t, cpuSnap, 4*gib, rangeMeta(), cpu)
	if cpu.Zones[0].Kind != "cpu" || cpu.Zones[0].GPUCount != 0 || !cpu.Zones[0].CurrentFit {
		t.Fatalf("cpu=%+v", cpu.Zones[0])
	}

	noFit := ComputePlacementRanges(hardware.Snapshot{}, 40*gib, rangeMeta(), 8192)
	if !noFit.Available || noFit.Zones[0].Kind != "no_fit" || noFit.Zones[0].CurrentFit {
		t.Fatalf("no fit=%+v", noFit)
	}
}

func TestComputePlacementRangesCurrentVsInstalledFit(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 32 * gib, RAMTotalBytes: 32 * gib,
		GPUs: []hardware.GPU{gpu("CUDA0", 3*gib, 24*gib)},
	}
	ranges := ComputePlacementRanges(snapshot, 4*gib, rangeMeta(), 4096)
	assertRangeInvariants(t, snapshot, 4*gib, rangeMeta(), ranges)
	if ranges.Zones[0].CurrentFit && ranges.Zones[0].Kind == "gpu" {
		t.Fatalf("occupied GPU should not currently be full gpu: %+v", ranges.Zones[0])
	}
	if !ranges.Zones[0].TotalHardwareFit {
		t.Fatalf("installed capacity should fit: %+v", ranges.Zones[0])
	}
}

func TestComputePlacementRangesUnavailable(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{RAMAvailableBytes: 16 * gib, GPUs: []hardware.GPU{gpu("CUDA0", 8*gib, 8*gib)}}
	unknown := ComputePlacementRanges(snapshot, 4*gib, rangeMeta(), 0)
	if unknown.Available || unknown.UnavailableReason == "" {
		t.Fatalf("unknown capability=%+v", unknown)
	}
	incomplete := ComputePlacementRanges(snapshot, 4*gib, Metadata{Architecture: "qwen2", ContextLength: 8192}, 8192)
	if incomplete.Available || !strings.Contains(incomplete.UnavailableReason, "reliable context boundaries") {
		t.Fatalf("incomplete metadata=%+v", incomplete)
	}
	tiny := ComputePlacementRanges(snapshot, 4*gib, rangeMeta(), 256)
	if tiny.Available || tiny.UnavailableReason == "" {
		t.Fatalf("tiny capability=%+v", tiny)
	}
}

func TestComputePlacementRangesMatchesLinearScan(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 64 * gib, RAMTotalBytes: 64 * gib,
		GPUs: []hardware.GPU{gpu("CUDA0", 8*gib, 8*gib), gpu("CUDA1", 8*gib, 8*gib), gpu("CUDA2", 8*gib, 8*gib)},
	}
	meta := rangeMeta()
	weights := 7 * gib
	capability := int64(16384)
	got := ComputePlacementRanges(snapshot, weights, meta, capability)
	assertRangeInvariants(t, snapshot, weights, meta, got)
	linear := linearPlacementRanges(snapshot, weights, meta, capability)
	if len(got.Zones) != len(linear.Zones) {
		t.Fatalf("zone count binary=%d linear=%d binary=%s linear=%s", len(got.Zones), len(linear.Zones), kindSequence(got), kindSequence(linear))
	}
	for i := range got.Zones {
		if got.Zones[i].StartContext != linear.Zones[i].StartContext || got.Zones[i].EndContext != linear.Zones[i].EndContext || got.Zones[i].Kind != linear.Zones[i].Kind || got.Zones[i].GPUCount != linear.Zones[i].GPUCount {
			t.Fatalf("zone %d binary=%+v linear=%+v", i, got.Zones[i], linear.Zones[i])
		}
	}
}

func TestAnalyzeAttachesPlacementRanges(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	model := models.Model{ID: "m1", TotalBytes: 4 * gib, Quantization: "Q4_K_M", ContextLength: 8192}
	path := writeMetadataGGUF(t, "qwen2", map[string]int64{
		"qwen2.context_length": 8192, "qwen2.block_count": 32, "qwen2.embedding_length": 4096,
		"qwen2.attention.head_count": 32, "qwen2.attention.head_count_kv": 8,
	})
	rec := Analyze(model, path, hardware.Snapshot{RAMAvailableBytes: 32 * gib, RAMTotalBytes: 32 * gib, GPUs: []hardware.GPU{gpu("CUDA0", 24*gib, 24*gib)}}, 4096, nil)
	if !rec.PlacementRanges.Available || len(rec.PlacementRanges.Zones) == 0 || rec.PlacementRanges.ContextStep != 512 {
		t.Fatalf("analyze ranges=%+v", rec.PlacementRanges)
	}
	cpuOnly := Analyze(model, path, hardware.Snapshot{RAMAvailableBytes: 32 * gib, RAMTotalBytes: 32 * gib}, 4096, nil)
	if cpuOnly.Offload.Mode != "cpu" || !cpuOnly.PlacementRanges.Available || cpuOnly.PlacementRanges.Zones[0].Kind != "cpu" {
		t.Fatalf("cpu-only ranges=%+v offload=%+v", cpuOnly.PlacementRanges, cpuOnly.Offload)
	}
}

func TestPlacementRangeHelpers(t *testing.T) {
	if alignContext(1000, 512) != 512 || alignContext(1536, 512) != 1536 || alignContext(10, 512) != 512 || alignContext(4096, 0) != 4096 {
		t.Fatal("align")
	}
	if alignContextDown(1000, 512) != 512 || alignContextDown(256, 512) != 0 || alignContextDown(4096, 0) != 4096 {
		t.Fatal("align down")
	}
	if itoa(0) != "0" || itoa(12) != "12" || itoa(-3) != "-3" {
		t.Fatal("itoa")
	}
	full := classifiedPlacement{Fit: true, Offload: Offload{Mode: "full", Devices: []string{"CUDA0"}, KVOnGPU: true}}
	kind, count := placementKind(full)
	if kind != "gpu" || count != 1 {
		t.Fatalf("full kind=%s count=%d", kind, count)
	}
	multi := classifiedPlacement{Fit: true, Offload: Offload{Mode: "multi_gpu", Devices: []string{"a", "b"}}}
	kind, count = placementKind(multi)
	if kind != "gpu" || count != 2 {
		t.Fatalf("multi kind=%s count=%d", kind, count)
	}
	bareMulti := classifiedPlacement{Fit: true, Offload: Offload{Mode: "multi_gpu"}}
	if _, count = placementKind(bareMulti); count != 2 {
		t.Fatalf("bare multi count=%d", count)
	}
	if offloadBool(true) != "1" || offloadBool(false) != "0" {
		t.Fatal("bool")
	}
}

func assertRangeInvariants(t *testing.T, snapshot hardware.Snapshot, weights int64, metadata Metadata, ranges PlacementRanges) {
	t.Helper()
	if !ranges.Available || len(ranges.Zones) == 0 {
		t.Fatalf("ranges unavailable: %+v", ranges)
	}
	if ranges.ContextStep != placementContextStep || ranges.MinimumContext != placementMinContext {
		t.Fatalf("step/min %+v", ranges)
	}
	if ranges.Zones[0].StartContext != ranges.MinimumContext || ranges.Zones[len(ranges.Zones)-1].EndContext != ranges.MaximumContext {
		t.Fatalf("coverage %+v", ranges)
	}
	prevEnd := int64(0)
	for i, zone := range ranges.Zones {
		if zone.StartContext%placementContextStep != 0 || zone.EndContext%placementContextStep != 0 || zone.EndContext < zone.StartContext {
			t.Fatalf("alignment %+v", zone)
		}
		if zone.StartContext < ranges.MinimumContext || zone.EndContext > ranges.MaximumContext {
			t.Fatalf("bounds %+v", zone)
		}
		if i > 0 && zone.StartContext != prevEnd+placementContextStep {
			t.Fatalf("gap/overlap prev=%d zone=%+v", prevEnd, zone)
		}
		start := classifyOffload(snapshot, weights, zone.StartContext, metadata)
		end := classifyOffload(snapshot, weights, zone.EndContext, metadata)
		startKind, startCount := placementKind(start)
		endKind, endCount := placementKind(end)
		if startKind != zone.Kind || endKind != zone.Kind || startCount != zone.GPUCount || endCount != zone.GPUCount {
			t.Fatalf("classify mismatch zone=%+v start=%s:%d end=%s:%d", zone, startKind, startCount, endKind, endCount)
		}
		if i+1 < len(ranges.Zones) {
			next := ranges.Zones[i+1]
			nextStart := classifyOffload(snapshot, weights, next.StartContext, metadata)
			nextKind, nextCount := placementKind(nextStart)
			if nextKind != next.Kind || nextCount != next.GPUCount {
				t.Fatalf("next start mismatch %+v got %s:%d", next, nextKind, nextCount)
			}
			if placementIdentity(end) == placementIdentity(nextStart) {
				t.Fatalf("adjacent identities equal %+v %+v", zone, next)
			}
		}
		prevEnd = zone.EndContext
	}
}

func assertKindSequence(t *testing.T, ranges PlacementRanges, want ...string) {
	t.Helper()
	got := kindSequence(ranges)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds got=%v want=%v zones=%s", got, want, formatZones(ranges))
	}
}

func assertKindSequenceContains(t *testing.T, ranges PlacementRanges, want ...string) {
	t.Helper()
	got := strings.Join(kindSequence(ranges), ",")
	for _, item := range want {
		if !strings.Contains(","+got+",", ","+item+",") {
			t.Fatalf("missing %s in %s (%s)", item, got, formatZones(ranges))
		}
	}
}

func kindSequence(ranges PlacementRanges) []string {
	out := make([]string, 0, len(ranges.Zones))
	for _, zone := range ranges.Zones {
		out = append(out, fmt.Sprintf("%s:%d", zone.Kind, zone.GPUCount))
	}
	return out
}

func formatZones(ranges PlacementRanges) string {
	parts := make([]string, 0, len(ranges.Zones))
	for _, zone := range ranges.Zones {
		parts = append(parts, fmt.Sprintf("%s:%d %d-%d devices=%v", zone.Kind, zone.GPUCount, zone.StartContext, zone.EndContext, zone.Devices))
	}
	return strings.Join(parts, " | ")
}

func linearPlacementRanges(snapshot hardware.Snapshot, weights int64, metadata Metadata, capability int64) PlacementRanges {
	maximum := alignContext(capability, placementContextStep)
	ranges := PlacementRanges{Available: true, MinimumContext: placementMinContext, MaximumContext: maximum, ContextStep: placementContextStep}
	var current classifiedPlacement
	var start int64
	for ctx := placementMinContext; ctx <= maximum; ctx += placementContextStep {
		classified := classifyOffload(snapshot, weights, ctx, metadata)
		if ctx == placementMinContext {
			current, start = classified, ctx
			continue
		}
		if placementIdentity(classified) != placementIdentity(current) {
			ranges.Zones = append(ranges.Zones, placementZoneFrom(start, ctx-placementContextStep, current))
			current, start = classified, ctx
		}
	}
	ranges.Zones = append(ranges.Zones, placementZoneFrom(start, maximum, current))
	return ranges
}
