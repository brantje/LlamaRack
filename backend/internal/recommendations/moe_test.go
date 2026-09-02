package recommendations

import (
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

func TestRecommendMoEOffloadUsesAllCurrentlyFreeGPUsAndMinimumSpill(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	metadata := Metadata{Architecture: "qwen3moe", BlockCount: 40, ExpertCount: 64}
	memory := MemoryEstimate{WeightsBytes: 20 * gib, KVCacheBytes: gib, RuntimeOverheadBytes: gib}
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 64 * gib,
		GPUs: []hardware.GPU{
			{ID: "CUDA0", FreeBytes: 8 * gib},
			{ID: "CUDA1", FreeBytes: 8 * gib},
			{ID: "CUDA2", FreeBytes: 256 * 1024 * 1024},
		},
	}
	fit, got := recommendMoEOffload(snapshot, memory, metadata)
	if !fit || got.Mode != "moe" || !got.KVOnGPU {
		t.Fatalf("unexpected recommendation fit=%v offload=%+v", fit, got)
	}
	if got.GPULayers != metadata.BlockCount {
		t.Fatalf("gpu_layers=%d want %d", got.GPULayers, metadata.BlockCount)
	}
	if len(got.Devices) != 2 || got.Devices[0] != "CUDA0" || got.Devices[1] != "CUDA1" {
		t.Fatalf("devices=%v want CUDA0,CUDA1", got.Devices)
	}
	if got.TensorSplit == "" {
		t.Fatal("multi-GPU MoE placement must include tensor split")
	}
	if got.NCPUMoe <= 0 {
		t.Fatalf("n_cpu_moe=%d want positive", got.NCPUMoe)
	}

	pooled := (8*gib - defaultVRAMReserve) * 2
	gpuNow, _ := scheduler.MoEWeightDistribution(memory.WeightsBytes, metadata.BlockCount, got.NCPUMoe, metadata.ExpertCount)
	if gpuNow+memory.KVCacheBytes+memory.RuntimeOverheadBytes > pooled {
		t.Fatal("returned MoE spill does not fit")
	}
	if got.NCPUMoe > 0 {
		gpuPrevious, _ := scheduler.MoEWeightDistribution(memory.WeightsBytes, metadata.BlockCount, got.NCPUMoe-1, metadata.ExpertCount)
		if gpuPrevious+memory.KVCacheBytes+memory.RuntimeOverheadBytes <= pooled {
			t.Fatalf("n_cpu_moe=%d was not minimal", got.NCPUMoe)
		}
	}
}

func TestRecommendMoEOffloadMovesKVOnlyAtCliff(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	metadata := Metadata{BlockCount: 40, ExpertCount: 64}
	memory := MemoryEstimate{WeightsBytes: 20 * gib, KVCacheBytes: 12 * gib, RuntimeOverheadBytes: gib}
	snapshot := hardware.Snapshot{RAMAvailableBytes: 64 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}, {ID: "CUDA1", FreeBytes: 8 * gib}}}
	fit, got := recommendMoEOffload(snapshot, memory, metadata)
	if !fit || got.Mode != "moe" || got.KVOnGPU {
		t.Fatalf("expected MoE KV-RAM cliff, fit=%v offload=%+v", fit, got)
	}
	if len(got.Devices) != 2 {
		t.Fatalf("KV cliff changed device set: %v", got.Devices)
	}
	if !strings.Contains(got.Reason, "KV cache") {
		t.Fatalf("reason does not explain KV placement: %q", got.Reason)
	}
}

func TestRecommendOffloadMoERequiresBinaryCapability(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	metadata := Metadata{BlockCount: 40, ExpertCount: 64}
	memory := MemoryEstimate{WeightsBytes: 12 * gib, KVCacheBytes: gib, RuntimeOverheadBytes: gib, CPUOnlyRAMBytes: 14 * gib, FullOffloadVRAMBytes: 14 * gib}
	snapshot := hardware.Snapshot{RAMAvailableBytes: 64 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib}, {ID: "CUDA1", FreeBytes: 8 * gib}}}

	fit, withCapability := recommendOffloadWithCapabilities(snapshot, memory, metadata, Capabilities{NCPUMoe: true})
	if !fit || withCapability.Mode != "moe" {
		t.Fatalf("supported profile should use MoE, fit=%v offload=%+v", fit, withCapability)
	}
	fit, withoutCapability := recommendOffloadWithCapabilities(snapshot, memory, metadata, Capabilities{})
	if !fit || withoutCapability.Mode == "moe" {
		t.Fatalf("unsupported profile must use dense fallback, fit=%v offload=%+v", fit, withoutCapability)
	}
	if !strings.Contains(withoutCapability.Reason, "does not advertise --n-cpu-moe") {
		t.Fatalf("fallback reason=%q", withoutCapability.Reason)
	}
}

func TestPlacementIdentityIgnoresNCPUMoeButTracksKVLocation(t *testing.T) {
	base := classifiedPlacement{Fit: true, Offload: Offload{Mode: "moe", Devices: []string{"CUDA0", "CUDA1"}, KVOnGPU: true, NCPUMoe: 8}}
	otherN := base
	otherN.Offload.NCPUMoe = 20
	if placementIdentity(base) != placementIdentity(otherN) {
		t.Fatal("n_cpu_moe must not fragment placement zones")
	}
	kvRAM := base
	kvRAM.Offload.KVOnGPU = false
	if placementIdentity(base) == placementIdentity(kvRAM) {
		t.Fatal("KV location must remain a placement-zone boundary")
	}
	kind, count := placementKind(base)
	if kind != "moe" || count != 2 {
		t.Fatalf("placement kind=(%q,%d) want (moe,2)", kind, count)
	}
}
