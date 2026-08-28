package recommendations

import (
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
)

func speedMetadata() Metadata {
	return Metadata{BlockCount: 32, Embedding: 4096, HeadCount: 32, KVHeadCount: 8}
}

func TestEstimateGenerationSpeedSingleGPU(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", MemoryBandwidthBytesPerSecond: 288_032_000_000}}}
	memory := MemoryEstimate{WeightsBytes: 12 * gib, KVCacheBytes: gib}
	offload := Offload{Mode: "full", Devices: []string{"CUDA0"}, KVOnGPU: true}
	guide := ClassifyQuantization("3.69BPW")

	got := estimateGenerationSpeed(snapshot, memory, offload, guide, speedMetadata())
	if !got.Estimated || got.MinTokensPerSecond <= 0 || got.MaxTokensPerSecond <= got.MinTokensPerSecond {
		t.Fatalf("estimate=%+v", got)
	}
	if !strings.Contains(got.Label, "tok/s") || !strings.Contains(got.Reason, "288 GB/s") || !strings.Contains(got.Reason, "12.0 GiB") || !strings.Contains(got.Reason, "1.0 GiB") {
		t.Fatalf("estimate=%+v", got)
	}
}

func TestEstimateGenerationSpeedMultiGPUDoesNotAddBandwidthNaively(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", MemoryBandwidthBytesPerSecond: 288_000_000_000},
		{ID: "CUDA1", MemoryBandwidthBytesPerSecond: 288_000_000_000},
	}}
	memory := MemoryEstimate{WeightsBytes: 20 * gib, KVCacheBytes: 2 * gib}
	guide := ClassifyQuantization("Q4_K_M")
	single := estimateGenerationSpeed(snapshot, memory, Offload{Mode: "full", Devices: []string{"CUDA0"}}, guide, speedMetadata())
	multi := estimateGenerationSpeed(snapshot, memory, Offload{Mode: "multi_gpu", Devices: []string{"CUDA0", "CUDA1"}, TensorSplit: "1,1"}, guide, speedMetadata())
	if !single.Estimated || !multi.Estimated {
		t.Fatalf("single=%+v multi=%+v", single, multi)
	}
	if multi.MaxTokensPerSecond >= single.MaxTokensPerSecond {
		t.Fatalf("multi-GPU estimate should include synchronization cost instead of summing bandwidth: single=%+v multi=%+v", single, multi)
	}
}

func TestEstimateGenerationSpeedHybridPlacements(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{
		RAMBandwidthBytesPerSecond: 52_000_000_000,
		GPUs: []hardware.GPU{{
			ID: "CUDA0",
			MemoryBandwidthBytesPerSecond: 288_000_000_000,
			PCIeBandwidthBytesPerSecond: 15_753_846_153,
		}},
	}
	memory := MemoryEstimate{WeightsBytes: 16 * gib, KVCacheBytes: 2 * gib}
	guide := ClassifyQuantization("Q4_K_M")
	metadata := speedMetadata()

	partial := estimateGenerationSpeed(snapshot, memory, Offload{Mode: "partial", Devices: []string{"CUDA0"}, GPULayers: 20, KVOnGPU: true}, guide, metadata)
	if !partial.Estimated || !strings.Contains(partial.Label, "tok/s") || !strings.Contains(partial.Reason, "measured memory-copy throughput") || !strings.Contains(partial.Reason, "PCIe") || !strings.Contains(partial.Reason, "VRAM traffic") {
		t.Fatalf("partial=%+v", partial)
	}

	hybrid := estimateGenerationSpeed(snapshot, memory, Offload{Mode: "hybrid", Devices: []string{"CUDA0"}, GPULayers: 20, KVOnGPU: false}, guide, metadata)
	if !hybrid.Estimated || !strings.Contains(hybrid.Reason, "system RAM traffic") {
		t.Fatalf("hybrid=%+v", hybrid)
	}
	if hybrid.MaxTokensPerSecond >= partial.MaxTokensPerSecond {
		t.Fatalf("moving KV traffic from VRAM to host RAM should reduce the estimate: partial=%+v hybrid=%+v", partial, hybrid)
	}
}

func TestEstimateGenerationSpeedHybridRequiresHostAndPCIeTelemetry(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	memory := MemoryEstimate{WeightsBytes: 8 * gib, KVCacheBytes: gib}
	guide := ClassifyQuantization("Q4_K_M")
	metadata := speedMetadata()
	offload := Offload{Mode: "partial", Devices: []string{"CUDA0"}, GPULayers: 16, KVOnGPU: true}
	gpu := hardware.GPU{ID: "CUDA0", MemoryBandwidthBytesPerSecond: 288_000_000_000, PCIeBandwidthBytesPerSecond: 15_753_846_153}

	missingRAM := estimateGenerationSpeed(hardware.Snapshot{GPUs: []hardware.GPU{gpu}}, memory, offload, guide, metadata)
	if missingRAM.Estimated || !strings.Contains(missingRAM.Reason, "host-memory bandwidth") {
		t.Fatalf("missing RAM=%+v", missingRAM)
	}
	gpu.PCIeBandwidthBytesPerSecond = 0
	missingPCIe := estimateGenerationSpeed(hardware.Snapshot{RAMBandwidthBytesPerSecond: 50_000_000_000, GPUs: []hardware.GPU{gpu}}, memory, offload, guide, metadata)
	if missingPCIe.Estimated || !strings.Contains(missingPCIe.Reason, "PCIe") {
		t.Fatalf("missing PCIe=%+v", missingPCIe)
	}
	missingMetadata := estimateGenerationSpeed(hardware.Snapshot{RAMBandwidthBytesPerSecond: 50_000_000_000, GPUs: []hardware.GPU{{ID: "CUDA0", MemoryBandwidthBytesPerSecond: 288_000_000_000, PCIeBandwidthBytesPerSecond: 15_753_846_153}}}, memory, offload, guide, Metadata{})
	if missingMetadata.Estimated || !strings.Contains(missingMetadata.Reason, "layer/embedding metadata") {
		t.Fatalf("missing metadata=%+v", missingMetadata)
	}
}

func TestEstimateGenerationSpeedRequiresMeasuredGPUBandwidth(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	memory := MemoryEstimate{WeightsBytes: 4 * gib, KVCacheBytes: gib}
	guide := ClassifyQuantization("Q4_K_M")
	missing := estimateGenerationSpeed(hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0"}}}, memory, Offload{Mode: "full", Devices: []string{"CUDA0"}}, guide, speedMetadata())
	if missing.Estimated || !strings.Contains(missing.Reason, "memory-bandwidth telemetry") {
		t.Fatalf("missing bandwidth=%+v", missing)
	}
	cpu := estimateGenerationSpeed(hardware.Snapshot{}, memory, Offload{Mode: "cpu"}, guide, speedMetadata())
	if cpu.Estimated || !strings.Contains(cpu.Reason, "CPU-only") {
		t.Fatalf("cpu=%+v", cpu)
	}
}

func TestEstimateGenerationSpeedCoversInvalidInputs(t *testing.T) {
	guide := ClassifyQuantization("Q4_K_M")
	bandwidthGPU := hardware.GPU{ID: "CUDA0", MemoryBandwidthBytesPerSecond: 288_000_000_000}
	metadata := speedMetadata()
	for name, got := range map[string]GenerationSpeedEstimate{
		"mode": estimateGenerationSpeed(hardware.Snapshot{}, MemoryEstimate{WeightsBytes: 1}, Offload{}, guide, metadata),
		"weights": estimateGenerationSpeed(hardware.Snapshot{GPUs: []hardware.GPU{bandwidthGPU}}, MemoryEstimate{}, Offload{Mode: "full", Devices: []string{"CUDA0"}}, guide, metadata),
		"devices": estimateGenerationSpeed(hardware.Snapshot{GPUs: []hardware.GPU{bandwidthGPU}}, MemoryEstimate{WeightsBytes: 1}, Offload{Mode: "full"}, guide, metadata),
		"missing device": estimateGenerationSpeed(hardware.Snapshot{GPUs: []hardware.GPU{bandwidthGPU}}, MemoryEstimate{WeightsBytes: 1}, Offload{Mode: "full", Devices: []string{"CUDA1"}}, guide, metadata),
	} {
		if got.Estimated || got.Label != "Estimate unavailable" {
			t.Fatalf("%s=%+v", name, got)
		}
	}
}

func TestGenerationSpeedHelpers(t *testing.T) {
	if low, high := quantizationBandwidthEfficiency("Q2_K"); low != 0.42 || high != 0.66 {
		t.Fatalf("Q2 efficiency=%v %v", low, high)
	}
	if low, high := quantizationBandwidthEfficiency("4.2BPW"); low != 0.48 || high != 0.73 {
		t.Fatalf("BPW efficiency=%v %v", low, high)
	}
	if low, high := quantizationBandwidthEfficiency("F16"); low != 0.58 || high != 0.82 {
		t.Fatalf("F16 efficiency=%v %v", low, high)
	}
	for _, tc := range []struct {
		value string
		count int
		want  []float64
	}{
		{"3,1", 2, []float64{0.75, 0.25}},
		{"bad", 2, []float64{0.5, 0.5}},
		{"", 1, []float64{1}},
	} {
		got := tensorSplitFractions(tc.value, tc.count)
		if len(got) != len(tc.want) {
			t.Fatalf("split %q=%v", tc.value, got)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("split %q=%v", tc.value, got)
			}
		}
	}
	if got := tensorSplitFractions("", 0); got != nil {
		t.Fatalf("zero devices=%v", got)
	}
	if estimatedHybridBoundaryTraffic(Metadata{Embedding: 4096}) != 32768 || estimatedHybridBoundaryTraffic(Metadata{}) != 0 {
		t.Fatal("unexpected hybrid boundary estimate")
	}
	if slowestPCIeBandwidth([]hardware.GPU{{PCIeBandwidthBytesPerSecond: 20}, {PCIeBandwidthBytesPerSecond: 10}}) != 10 {
		t.Fatal("unexpected PCIe bottleneck")
	}
	if got := formatTPSRange(4.2, 6.7); got != "~4.2–6.7 tok/s" {
		t.Fatalf("range=%q", got)
	}
	if got := formatTPSRange(12, 18); got != "~12–18 tok/s" {
		t.Fatalf("range=%q", got)
	}
	if got := formatTPSRange(0.01, 0.05); got != "<0.1 tok/s estimated" {
		t.Fatalf("range=%q", got)
	}
}
