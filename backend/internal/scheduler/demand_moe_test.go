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
		weights   int64
		blocks    int64
		cpuBlocks int64
		experts   int64
		wantGPU   int64
		wantHost  int64
	}{
		"no weights":        {weights: 0, blocks: 10, cpuBlocks: 5, experts: 8, wantGPU: 0, wantHost: 0},
		"no blocks":         {weights: 1000, blocks: 0, cpuBlocks: 5, experts: 8, wantGPU: 1000, wantHost: 0},
		"no cpu blocks":     {weights: 1000, blocks: 10, cpuBlocks: 0, experts: 8, wantGPU: 1000, wantHost: 0},
		"negative cpu":      {weights: 1000, blocks: 10, cpuBlocks: -1, experts: 8, wantGPU: 1000, wantHost: 0},
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

func TestCombinedMoEWeightDistributionUnionsPartialLayersAndExpertSpill(t *testing.T) {
	const weights = int64(20 * 1024 * 1024 * 1024)
	gpu, host := CombinedMoEWeightDistribution(weights, 40, 20, 30, 64)
	wantHost := int64(15300820992) // 50% full CPU layers + 21.25% extra expert spill
	if host != wantHost || gpu != weights-wantHost {
		t.Fatalf("union distribution gpu=%d host=%d want gpu=%d host=%d", gpu, host, weights-wantHost, wantHost)
	}
}

func TestCombinedMoEWeightDistributionBoundaries(t *testing.T) {
	for name, tc := range map[string]struct {
		weights   int64
		blocks    int64
		gpuLayers int64
		cpuMoe    int64
		experts   int64
		wantGPU   int64
		wantHost  int64
	}{
		"no weights":        {weights: 0, blocks: 10, gpuLayers: 10, cpuMoe: 5, experts: 8, wantGPU: 0, wantHost: 0},
		"no blocks":         {weights: 1000, blocks: 0, gpuLayers: 0, cpuMoe: 5, experts: 8, wantGPU: 1000, wantHost: 0},
		"full gpu matches":  {weights: 1000, blocks: 10, gpuLayers: 10, cpuMoe: 5, experts: 64},
		"gpu layers capped": {weights: 1000, blocks: 10, gpuLayers: 99, cpuMoe: 5, experts: 64},
		"cpu moe capped":    {weights: 1000, blocks: 10, gpuLayers: 10, cpuMoe: 99, experts: 64},
		"negative gpu":      {weights: 1000, blocks: 10, gpuLayers: -1, cpuMoe: 0, experts: 8, wantGPU: 0, wantHost: 1000},
	} {
		t.Run(name, func(t *testing.T) {
			wantGPU, wantHost := tc.wantGPU, tc.wantHost
			if name == "full gpu matches" || name == "gpu layers capped" {
				wantGPU, wantHost = MoEWeightDistribution(tc.weights, tc.blocks, tc.cpuMoe, tc.experts)
			}
			if name == "cpu moe capped" {
				wantGPU, wantHost = MoEWeightDistribution(tc.weights, tc.blocks, tc.blocks, tc.experts)
			}
			gpu, host := CombinedMoEWeightDistribution(tc.weights, tc.blocks, tc.gpuLayers, tc.cpuMoe, tc.experts)
			if gpu != wantGPU || host != wantHost {
				t.Fatalf("distribution=(%d,%d) want (%d,%d)", gpu, host, wantGPU, wantHost)
			}
		})
	}
}

func TestEstimateDemandMixedGpuLayersAndCpuMoeUsesUnion(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	meta := KVMetadata{Architecture: "qwen3moe", BlockCount: 40, Embedding: 4096, HeadCount: 32, KVHeadCount: 8, ExpertCount: 64}

	t.Run("n-cpu-moe extends into GPU layers", func(t *testing.T) {
		got := EstimateDemand(DemandInput{
			WeightsBytes: 20 * gib, Context: 4096, Metadata: meta,
			Options: map[string]string{"n-gpu-layers": "20", "n-cpu-moe": "30"},
		})
		_, wantHost := CombinedMoEWeightDistribution(20*gib, 40, 20, 30, 64)
		if got.HostRAMBytes != wantHost {
			t.Fatalf("host=%d want union %d (not max of dense/moe)", got.HostRAMBytes, wantHost)
		}
		maxOnly, _ := MoEWeightDistribution(20*gib, 40, 30, 64)
		denseHost := 10 * gib
		if got.HostRAMBytes <= denseHost || got.HostRAMBytes <= (20*gib-maxOnly) {
			t.Fatalf("union host=%d should exceed both dense-only %d and moe-only %d", got.HostRAMBytes, denseHost, 20*gib-maxOnly)
		}
	})

	t.Run("n-cpu-moe fully inside CPU layers", func(t *testing.T) {
		dense := EstimateDemand(DemandInput{
			WeightsBytes: 20 * gib, Context: 4096, Metadata: meta,
			Options: map[string]string{"n-gpu-layers": "20"},
		})
		got := EstimateDemand(DemandInput{
			WeightsBytes: 20 * gib, Context: 4096, Metadata: meta,
			Options: map[string]string{"n-gpu-layers": "20", "n-cpu-moe": "10"},
		})
		if got.HostRAMBytes != dense.HostRAMBytes || got.VRAMBytes() != dense.VRAMBytes() {
			t.Fatalf("fully overlapping cpu-moe must not add host bytes: dense=%+v got=%+v", dense, got)
		}
	})

	t.Run("full n-gpu-layers matches pure MoE", func(t *testing.T) {
		got := EstimateDemand(DemandInput{
			WeightsBytes: 20 * gib, Context: 4096, Metadata: meta,
			Options: map[string]string{"n-gpu-layers": "40", "n-cpu-moe": "20"},
		})
		pure := EstimateDemand(DemandInput{
			WeightsBytes: 20 * gib, Context: 4096, Metadata: meta,
			Options: map[string]string{"n-cpu-moe": "20"},
		})
		if got.HostRAMBytes != pure.HostRAMBytes || got.VRAMBytes() != pure.VRAMBytes() {
			t.Fatalf("full GPU layers must match pure MoE: got=%+v pure=%+v", got, pure)
		}
		_, wantHost := MoEWeightDistribution(20*gib, 40, 20, 64)
		if got.HostRAMBytes != wantHost {
			t.Fatalf("host=%d want MoEWeightDistribution %d", got.HostRAMBytes, wantHost)
		}
	})

	t.Run("cpu-only caps host weights at all weights", func(t *testing.T) {
		got := EstimateDemand(DemandInput{
			WeightsBytes: 20 * gib, Context: 4096, Metadata: meta,
			Options: map[string]string{"n-gpu-layers": "0", "n-cpu-moe": "30"},
		})
		if got.VRAMBytes() != 0 {
			t.Fatalf("cpu-only VRAM=%d want 0", got.VRAMBytes())
		}
		if got.HostRAMBytes < 20*gib {
			t.Fatalf("cpu-only host=%d want at least all weights", got.HostRAMBytes)
		}
		denseCPU := EstimateDemand(DemandInput{
			WeightsBytes: 20 * gib, Context: 4096, Metadata: meta,
			Options: map[string]string{"n-gpu-layers": "0"},
		})
		if got.HostRAMBytes != denseCPU.HostRAMBytes {
			t.Fatalf("MoE cannot add beyond all weights: got=%d denseCPU=%d", got.HostRAMBytes, denseCPU.HostRAMBytes)
		}
	})

	t.Run("cpu-moe true with partial n-gpu-layers", func(t *testing.T) {
		got := EstimateDemand(DemandInput{
			WeightsBytes: 20 * gib, Context: 4096, Metadata: meta,
			Options: map[string]string{"n-gpu-layers": "20", "cpu-moe": "true"},
		})
		_, wantHost := CombinedMoEWeightDistribution(20*gib, 40, 20, 40, 64)
		if got.HostRAMBytes != wantHost {
			t.Fatalf("cpu-moe host=%d want union %d", got.HostRAMBytes, wantHost)
		}
	})
}
