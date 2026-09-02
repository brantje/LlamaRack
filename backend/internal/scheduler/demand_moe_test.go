package scheduler

import "testing"

func TestEstimateDemandMoEExpertOffloadMovesWeightsToHostRAM(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	meta := KVMetadata{
		Architecture: "qwen3moe",
		BlockCount:   40,
		Embedding:    4096,
		HeadCount:    32,
		KVHeadCount:  8,
		ExpertCount:  64,
	}
	base := EstimateDemand(DemandInput{WeightsBytes: 20 * gib, Context: 4096, Metadata: meta})
	half := EstimateDemand(DemandInput{WeightsBytes: 20 * gib, Context: 4096, Metadata: meta, Options: map[string]string{"n-cpu-moe": "20"}})
	all := EstimateDemand(DemandInput{WeightsBytes: 20 * gib, Context: 4096, Metadata: meta, Options: map[string]string{"cpu-moe": "true"}})

	if !(base.VRAMBytes() > half.VRAMBytes() && half.VRAMBytes() > all.VRAMBytes()) {
		t.Fatalf("expected VRAM to fall as experts move to RAM: base=%d half=%d all=%d", base.VRAMBytes(), half.VRAMBytes(), all.VRAMBytes())
	}
	if !(base.HostRAMBytes < half.HostRAMBytes && half.HostRAMBytes < all.HostRAMBytes) {
		t.Fatalf("expected host RAM to rise as experts move to RAM: base=%d half=%d all=%d", base.HostRAMBytes, half.HostRAMBytes, all.HostRAMBytes)
	}
	if all.VRAMBytes() <= 0 {
		t.Fatalf("cpu-moe must retain shared/non-expert weights on GPU, got %d", all.VRAMBytes())
	}
}

func TestEstimateDemandMoEAndKVOffloadStack(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	meta := KVMetadata{Architecture: "qwen3moe", BlockCount: 32, Embedding: 4096, HeadCount: 32, KVHeadCount: 8, ExpertCount: 8}
	onGPU := EstimateDemand(DemandInput{WeightsBytes: 12 * gib, Context: 32768, Metadata: meta, Options: map[string]string{"n-cpu-moe": "16"}})
	kvRAM := EstimateDemand(DemandInput{WeightsBytes: 12 * gib, Context: 32768, Metadata: meta, Options: map[string]string{"n-cpu-moe": "16", "no-kv-offload": "true"}})

	if onGPU.KVCacheBytes <= 0 {
		t.Fatal("expected non-zero KV estimate")
	}
	if onGPU.VRAMBytes()-kvRAM.VRAMBytes() != onGPU.KVCacheBytes {
		t.Fatalf("KV offload VRAM delta=%d want %d", onGPU.VRAMBytes()-kvRAM.VRAMBytes(), onGPU.KVCacheBytes)
	}
	if kvRAM.HostRAMBytes-onGPU.HostRAMBytes != onGPU.KVCacheBytes {
		t.Fatalf("KV offload RAM delta=%d want %d", kvRAM.HostRAMBytes-onGPU.HostRAMBytes, onGPU.KVCacheBytes)
	}
}

func TestEstimateDemandInvalidMoEOptionDoesNotUnderReserve(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	meta := KVMetadata{BlockCount: 32, Embedding: 4096, HeadCount: 32, ExpertCount: 8}
	base := EstimateDemand(DemandInput{WeightsBytes: 8 * gib, Metadata: meta})
	for name, value := range map[string]string{"nonnumeric": "wat", "negative": "-1", "too-large": "33"} {
		t.Run(name, func(t *testing.T) {
			got := EstimateDemand(DemandInput{WeightsBytes: 8 * gib, Metadata: meta, Options: map[string]string{"n-cpu-moe": value}})
			if got.VRAMBytes() != base.VRAMBytes() || got.HostRAMBytes != base.HostRAMBytes {
				t.Fatalf("invalid n-cpu-moe=%q changed demand: base=%+v got=%+v", value, base, got)
			}
		})
	}
}

func TestMoEWeightDistributionUsesLockedExpertShareHeuristic(t *testing.T) {
	const weights = int64(1000)
	gpu, host := MoEWeightDistribution(weights, 10, 10, 64)
	if gpu != 150 || host != 850 {
		t.Fatalf("64 experts distribution=(%d,%d) want (150,850)", gpu, host)
	}
	gpu, host = MoEWeightDistribution(weights, 10, 10, 2)
	if gpu != 500 || host != 500 {
		t.Fatalf("2 experts distribution=(%d,%d) want (500,500)", gpu, host)
	}
	gpu, host = MoEWeightDistribution(weights, 10, 5, 1)
	if gpu != 625 || host != 375 {
		t.Fatalf("single expert half-block distribution=(%d,%d) want (625,375)", gpu, host)
	}
}

func TestMoEWeightDistributionBoundaryInputs(t *testing.T) {
	for name, tc := range map[string]struct {
		weights     int64
		blocks      int64
		cpuBlocks   int64
		experts     int64
		wantGPU     int64
		wantHost    int64
	}{
		"no weights":       {weights: 0, blocks: 10, cpuBlocks: 5, experts: 8, wantGPU: 0, wantHost: 0},
		"no blocks":        {weights: 1000, blocks: 0, cpuBlocks: 5, experts: 8, wantGPU: 1000, wantHost: 0},
		"no cpu blocks":    {weights: 1000, blocks: 10, cpuBlocks: 0, experts: 8, wantGPU: 1000, wantHost: 0},
		"negative cpu":     {weights: 1000, blocks: 10, cpuBlocks: -1, experts: 8, wantGPU: 1000, wantHost: 0},
		"cpu blocks capped": {weights: 1000, blocks: 10, cpuBlocks: 99, experts: 8, wantGPU: 150, wantHost: 850},
	} {
		t.Run(name, func(t *testing.T) {
			gpu, host := MoEWeightDistribution(tc.weights, tc.blocks, tc.cpuBlocks, tc.experts)
			if gpu != tc.wantGPU || host != tc.wantHost {
				t.Fatalf("distribution=(%d,%d) want (%d,%d)", gpu, host, tc.wantGPU, tc.wantHost)
			}
		})
	}
}

func TestExpertWeightShareBoundaries(t *testing.T) {
	for experts, want := range map[int64]float64{
		0:  0.75,
		1:  0.75,
		2:  0.50,
		4:  0.75,
		64: 0.85,
	} {
		if got := ExpertWeightShare(experts); got != want {
			t.Fatalf("ExpertWeightShare(%d)=%v want %v", experts, got, want)
		}
	}
}
