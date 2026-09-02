package recommendations

import (
	"math"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

func TestMoERecommendationHelperCoverage(t *testing.T) {
	cases := map[string]int64{
		"":                    0,
		"0":                   0,
		"17":                  17,
		" 42 ":                42,
		"-1":                  0,
		"12x":                 0,
		"9223372036854775808": 0,
	}
	for input, want := range cases {
		if got := parseMetadataInt(input); got != want {
			t.Fatalf("parseMetadataInt(%q)=%d want %d", input, got, want)
		}
	}

	if split := moeTensorSplit([]int64{1024}); split != "" {
		t.Fatalf("single-device tensor split=%q want empty", split)
	}
	if split := moeTensorSplit([]int64{0, 1024}); split != "1,1" {
		t.Fatalf("small tensor split=%q want 1,1", split)
	}

	if fit, offload := recommendMoEOffload(hardware.Snapshot{}, MemoryEstimate{}, Metadata{}); fit || offload.Mode != "" {
		t.Fatalf("invalid MoE inputs must not fit: fit=%v offload=%+v", fit, offload)
	}
	if fit, offload := recommendMoEOffload(
		hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: defaultVRAMReserve}}},
		MemoryEstimate{WeightsBytes: 1024},
		Metadata{BlockCount: 1, ExpertCount: 2},
	); fit || offload.Mode != "" {
		t.Fatalf("no usable GPU headroom must not fit: fit=%v offload=%+v", fit, offload)
	}

	if splitFitsDevices(1, nil) {
		t.Fatal("empty device list must not fit")
	}
	if splitFitsDevices(2, []int64{1}) {
		t.Fatal("single GPU below demand must not fit")
	}
	if !splitFitsDevices(2, []int64{2}) {
		t.Fatal("single GPU meeting demand must fit")
	}
	unit := 256 * mib
	if splitFitsDevices(4*unit, []int64{2 * unit, unit}) {
		t.Fatal("last-device remainder overcommit must fail")
	}

	const gib = int64(1024 * 1024 * 1024)
	fit, offload := recommendMoEOffload(
		hardware.Snapshot{RAMAvailableBytes: 64 * gib, GPUs: []hardware.GPU{
			{ID: "CUDA0", FreeBytes: defaultVRAMReserve + mib},
			{ID: "CUDA1", FreeBytes: defaultVRAMReserve + mib},
		}},
		MemoryEstimate{WeightsBytes: 20 * gib, KVCacheBytes: gib, RuntimeOverheadBytes: gib},
		Metadata{BlockCount: 40, ExpertCount: 64},
	)
	if fit || offload.Mode != "" {
		t.Fatalf("pooled miss must not recommend MoE: fit=%v offload=%+v", fit, offload)
	}
}

func TestCompatibilityRecommendationWrappers(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	metadata := Metadata{BlockCount: 8, Embedding: 1024, HeadCount: 8, KVHeadCount: 8}
	memory := estimateMemory(gib, 4096, metadata)
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 8 * gib,
		RAMTotalBytes:     8 * gib,
		GPUs:              []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib, TotalBytes: 8 * gib}},
	}

	fit, offload := recommendOffload(snapshot, memory, metadata)
	if !fit || offload.Mode != "full" {
		t.Fatalf("compat recommendOffload fit=%v offload=%+v", fit, offload)
	}
	classified := classifyOffload(snapshot, gib, 4096, metadata)
	if !classified.Fit || classified.Offload.Mode != "full" {
		t.Fatalf("compat classifyOffload=%+v", classified)
	}
	if !totalHardwareFit(snapshot, gib, 4096, metadata) {
		t.Fatal("compat totalHardwareFit should fit")
	}
	fit, offload = discoverOffload(snapshot, memory, metadata)
	if !fit || offload.Mode != "full" {
		t.Fatalf("compat discoverOffload fit=%v offload=%+v", fit, offload)
	}

	noGPU := hardware.Snapshot{RAMAvailableBytes: 8 * gib}
	fit, offload = discoverOffload(noGPU, memory, metadata)
	if !fit || offload.Mode != "cpu" {
		t.Fatalf("CPU discover fallback fit=%v offload=%+v", fit, offload)
	}
	fit, _ = discoverOffload(hardware.Snapshot{RAMAvailableBytes: 1}, memory, metadata)
	if fit {
		t.Fatal("insufficient CPU discover fallback must not fit")
	}
}

func TestAnalyzeMoEConfidenceAndIntegerOverflow(t *testing.T) {
	if got := parseMetadataInt("9223372036854775807"); got != math.MaxInt64 {
		t.Fatalf("max int64 parse=%d", got)
	}
	metadata := Metadata{Architecture: "moe", BlockCount: 4, Embedding: 128, HeadCount: 4, ExpertCount: 8}
	if confidence(metadata, nil) != "high" {
		t.Fatal("metadata should begin with high confidence before MoE downgrade in Analyze")
	}
}
