package recommendations

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/scheduler"
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


func TestAnalyzeRuntimeUsesCanonicalRuntimePlanner(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	path := writeMetadataGGUF(t, "qwen2", map[string]int64{
		"qwen2.context_length": 32768, "qwen2.block_count": 24, "qwen2.embedding_length": 2048,
		"qwen2.attention.head_count": 16, "qwen2.attention.head_count_kv": 8,
	})
	model := models.Model{ID: "runtime", TotalBytes: 4 * gib, Quantization: "Q4_K_M", ContextLength: 32768}
	snapshot := hardware.Snapshot{
		RAMTotalBytes: 32 * gib, RAMAvailableBytes: 24 * gib,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib, TotalBytes: 8 * gib}},
	}
	runtime := RuntimeConfig{
		GPUMode: "auto",
		Options: map[string]string{"ctx-size": "4096"},
		AllowSystemSpillover: true,
	}
	rec := AnalyzeRuntime(model, path, snapshot, 4096, nil, Capabilities{GPULayers: true, NoKVOffload: true}, runtime)
	if !rec.CurrentFit || !rec.TotalHardwareFit || !rec.CPUFit {
		t.Fatalf("fit flags=%+v", rec)
	}
	if rec.Offload.Mode != "full" || len(rec.Offload.Devices) != 1 || rec.Offload.Devices[0] != "CUDA0" || !rec.Offload.KVOnGPU {
		t.Fatalf("offload=%+v", rec.Offload)
	}
	if rec.Memory.WeightsBytes != model.TotalBytes || rec.Memory.KVCacheBytes <= 0 || rec.Memory.RuntimeOverheadBytes <= 0 ||
		rec.Memory.FullOffloadVRAMBytes <= model.TotalBytes || rec.Memory.CPUOnlyRAMBytes <= model.TotalBytes {
		t.Fatalf("memory=%+v", rec.Memory)
	}
	if !rec.PlacementRanges.Available || rec.PlacementRanges.MaximumContext == 0 || rec.ContextCapability != 32768 || rec.ContextAssumed {
		t.Fatalf("context/ranges=%+v", rec)
	}
	if rec.Confidence != "high" || !strings.Contains(rec.Quantization.Summary, "Balanced") {
		t.Fatalf("metadata/quantization=%+v", rec)
	}
}

func TestAnalyzeRuntimeGracefulHardwareAndMetadataFallbacks(t *testing.T) {
	const gib int64 = 1024 * 1024 * 1024
	good := writeMetadataGGUF(t, "qwen2", map[string]int64{
		"qwen2.context_length": 32768, "qwen2.block_count": 8, "qwen2.embedding_length": 1024,
		"qwen2.attention.head_count": 8, "qwen2.attention.head_count_kv": 8,
	})
	model := models.Model{ID: "fallback", TotalBytes: 2 * gib, Quantization: "Q8_0"}
	rec := AnalyzeRuntime(model, good, hardware.Snapshot{}, 0, errors.New("probe failed"), Capabilities{}, RuntimeConfig{GPUMode: "auto"})
	if rec.HardwareWarning != "probe failed" || rec.Offload.Mode != "cpu" || rec.PlacementRanges.Available ||
		rec.PlacementRanges.UnavailableReason != "Hardware availability is unknown." || !rec.ContextAssumed {
		t.Fatalf("hardware fallback=%+v", rec)
	}

	bad := filepath.Join(t.TempDir(), "bad.gguf")
	if err := os.WriteFile(bad, []byte("not gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = AnalyzeRuntime(models.Model{ID: "bad", TotalBytes: gib}, bad,
		hardware.Snapshot{RAMTotalBytes: 16 * gib, RAMAvailableBytes: 12 * gib}, 4096, nil,
		Capabilities{GPULayers: true}, RuntimeConfig{GPUMode: "auto", AllowSystemSpillover: true})
	if rec.MetadataWarning == "" || rec.Confidence != "low" || rec.PlacementRanges.Available ||
		rec.PlacementRanges.UnavailableReason == "" {
		t.Fatalf("metadata fallback=%+v", rec)
	}
}

func TestRuntimeCompanionAndReasonHelpers(t *testing.T) {
	dir := t.TempDir()
	mmproj := filepath.Join(dir, "mmproj.gguf")
	draft := filepath.Join(dir, "draft.gguf")
	if err := os.WriteFile(mmproj, make([]byte, 17), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draft, make([]byte, 23), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CompanionBytes(map[string]string{
		"--mmproj": mmproj, "spec-draft-model": draft,
	}); got != 40 {
		t.Fatalf("companion bytes=%d", got)
	}
	if got := CompanionBytes(map[string]string{
		"mmproj": filepath.Join(dir, "missing.gguf"), "spec-draft-model": dir,
	}); got != 0 {
		t.Fatalf("ignored companion bytes=%d", got)
	}

	reasons := map[string]string{
		"full": "one GPU", "multi_gpu": "GPU set", "moe": "expert weights",
		"partial": "part of the model weights", "hybrid": "moving KV", "cpu": "host RAM",
		"other": "selected runtime policy",
	}
	for mode, want := range reasons {
		got := runtimePlanReason(scheduler.RuntimePlan{Fits: true, Mode: mode})
		if !strings.Contains(got, want) {
			t.Fatalf("mode=%s reason=%q", mode, got)
		}
	}
	if got := runtimePlanReason(scheduler.RuntimePlan{Fits: false}); !strings.Contains(got, "cannot be admitted") {
		t.Fatalf("no-fit reason=%q", got)
	}
	if runtimeOptionEnabled(map[string]string{"--cpu-moe": "YES"}, "cpu-moe") != true ||
		runtimeOptionEnabled(map[string]string{"cpu-moe": "off"}, "cpu-moe") {
		t.Fatal("runtime option boolean parsing")
	}
}

func TestOffloadFromRuntimePlanOptionModes(t *testing.T) {
	meta := Metadata{BlockCount: 24}
	plan := scheduler.RuntimePlan{
		Fits: true, Mode: "partial",
		Placement: scheduler.Placement{Devices: []string{"CUDA0"}, TensorSplit: "1"},
		Options: map[string]string{"n-gpu-layers": "7", "n-cpu-moe": "3", "no-kv-offload": "true"},
	}
	got := offloadFromRuntimePlan(plan, meta)
	if got.GPULayers != 7 || got.NCPUMoe != 3 || got.KVOnGPU || got.TensorSplit != "1" || len(got.Devices) != 1 {
		t.Fatalf("offload=%+v", got)
	}
	plan.Options = map[string]string{"gpu-layers": "-1", "cpu-moe": "true"}
	got = offloadFromRuntimePlan(plan, meta)
	if got.GPULayers != 24 || got.NCPUMoe != 24 || !got.KVOnGPU {
		t.Fatalf("full option offload=%+v", got)
	}
}


func TestOffloadFromRuntimePlanCPUIsSemanticallyCPUOnly(t *testing.T) {
	meta := Metadata{BlockCount: 24}
	got := offloadFromRuntimePlan(scheduler.RuntimePlan{
		Fits: true,
		Mode: "cpu",
		Placement: scheduler.Placement{Devices: []string{"CUDA0"}, TensorSplit: "1"},
		Options: map[string]string{},
	}, meta)
	if got.GPULayers != 0 || len(got.Devices) != 0 || got.TensorSplit != "" || got.KVOnGPU {
		t.Fatalf("cpu offload metadata=%+v", got)
	}
}
