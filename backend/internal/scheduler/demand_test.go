package scheduler

import "testing"

func TestEstimateDemandIncludesKVOverheadAndContext(t *testing.T) {
	meta := KVMetadata{BlockCount: 2, Embedding: 16, HeadCount: 4, KVHeadCount: 4}
	small := EstimateDemand(DemandInput{WeightsBytes: 8 * 1024 * 1024 * 1024, Context: 4096, Metadata: meta})
	large := EstimateDemand(DemandInput{WeightsBytes: 8 * 1024 * 1024 * 1024, Context: 32768, Metadata: meta})
	if small.KVCacheBytes <= 0 || small.RuntimeOverheadBytes < 256*demandMiB {
		t.Fatalf("small=%+v", small)
	}
	if large.KVCacheBytes <= small.KVCacheBytes {
		t.Fatalf("larger context should increase KV: small=%d large=%d", small.KVCacheBytes, large.KVCacheBytes)
	}
	if small.VRAMBytes() >= large.VRAMBytes() {
		t.Fatalf("larger context should increase VRAM demand")
	}
	if small.Confidence != "high" {
		t.Fatalf("confidence=%s", small.Confidence)
	}
}

func TestEstimateDemandMissingMetadataFallsBack(t *testing.T) {
	demand := EstimateDemand(DemandInput{WeightsBytes: 1024, Context: 8192, MetadataErr: errDemand("no meta")})
	if demand.KVCacheBytes != 0 || demand.Confidence != "low" || demand.RuntimeOverheadBytes != 256*demandMiB {
		t.Fatalf("fallback=%+v", demand)
	}
	if demand.VRAMBytes() != 1024+256*demandMiB {
		t.Fatalf("fallback vram=%d", demand.VRAMBytes())
	}
}

func TestEstimateDemandOptionsAffectPlacement(t *testing.T) {
	meta := KVMetadata{BlockCount: 32, Embedding: 4096, HeadCount: 32, KVHeadCount: 8}
	base := EstimateDemand(DemandInput{WeightsBytes: 4 * 1024 * 1024 * 1024, Context: 4096, Metadata: meta})
	quant := EstimateDemand(DemandInput{
		WeightsBytes: 4 * 1024 * 1024 * 1024, Context: 4096, Metadata: meta,
		Options: map[string]string{"cache-type-k": "q8_0", "cache-type-v": "q8_0"},
	})
	if quant.KVCacheBytes >= base.KVCacheBytes {
		t.Fatalf("q8 cache should shrink KV: base=%d quant=%d", base.KVCacheBytes, quant.KVCacheBytes)
	}
	cpu := EstimateDemand(DemandInput{
		WeightsBytes: 4 * 1024 * 1024 * 1024, Context: 4096, Metadata: meta,
		Options: map[string]string{"gpu-layers": "0"},
	})
	if cpu.VRAMBytes() != 0 || cpu.HostRAMBytes <= 0 {
		t.Fatalf("cpu-only=%+v", cpu)
	}
	partial := EstimateDemand(DemandInput{
		WeightsBytes: 4 * 1024 * 1024 * 1024, Context: 4096, Metadata: meta,
		Options: map[string]string{"gpu-layers": "16"},
	})
	if partial.VRAMBytes() >= base.VRAMBytes() || partial.HostRAMBytes <= 0 {
		t.Fatalf("partial=%+v baseVRAM=%d", partial, base.VRAMBytes())
	}
	ramKV := EstimateDemand(DemandInput{
		WeightsBytes: 4 * 1024 * 1024 * 1024, Context: 4096, Metadata: meta,
		Options: map[string]string{"no-kv-offload": "true"},
	})
	if ramKV.KVCacheBytes != base.KVCacheBytes || ramKV.VRAMBytes() >= base.VRAMBytes() || ramKV.HostRAMBytes != ramKV.KVCacheBytes {
		t.Fatalf("no-kv-offload=%+v base=%+v", ramKV, base)
	}
	fromOpt := EstimateDemand(DemandInput{WeightsBytes: 1024, Metadata: meta, Options: map[string]string{"ctx-size": "8192"}})
	explicit := EstimateDemand(DemandInput{WeightsBytes: 1024, Context: 8192, Metadata: meta})
	if fromOpt.KVCacheBytes != explicit.KVCacheBytes {
		t.Fatalf("ctx-size option=%d explicit=%d", fromOpt.KVCacheBytes, explicit.KVCacheBytes)
	}
}

func TestEstimateDemandNegativeWeightsAndUnknownCache(t *testing.T) {
	demand := EstimateDemand(DemandInput{WeightsBytes: -5, Context: 4096})
	if demand.WeightsBytes != 0 || demand.Confidence != "low" {
		t.Fatalf("negative=%+v", demand)
	}
	meta := KVMetadata{BlockCount: 1, Embedding: 8, HeadCount: 2}
	unknown := EstimateDemand(DemandInput{WeightsBytes: 1, Context: 8, Metadata: meta, Options: map[string]string{"cache-type-k": "mystery"}})
	f16 := EstimateDemand(DemandInput{WeightsBytes: 1, Context: 8, Metadata: meta})
	if unknown.KVCacheBytes != f16.KVCacheBytes {
		t.Fatalf("unknown cache should stay conservative f16: %d vs %d", unknown.KVCacheBytes, f16.KVCacheBytes)
	}
	if estimateKVBytes(4096, KVMetadata{BlockCount: 2, Embedding: 2, HeadCount: 4}, nil) != 0 {
		t.Fatal("invalid head dim")
	}
	if f32 := cacheElementBytes(map[string]string{"cache-type-k": "f32"}, "cache-type-k"); f32 != 4 {
		t.Fatalf("f32 bytes=%d", f32)
	}
	if f16 := cacheElementBytes(map[string]string{"cache-type-k": "bf16"}, "cache-type-k"); f16 != 2 {
		t.Fatalf("bf16 bytes=%d", f16)
	}
	if parseContextOption(map[string]string{"ctx-size": "nope"}) != 0 || parseContextOption(nil) != 0 {
		t.Fatal("invalid ctx-size")
	}
	if demandConfidence(KVMetadata{Architecture: "llama"}, errDemand("partial")) != "medium" {
		t.Fatal("medium confidence")
	}
	if !kvOnGPU(map[string]string{"no-kv-offload": "no"}, false) {
		t.Fatal("kv should stay on GPU")
	}
	fromFlag := EstimateDemand(DemandInput{WeightsBytes: 1024, Metadata: meta, Options: map[string]string{"--ctx-size": "8192"}})
	explicit := EstimateDemand(DemandInput{WeightsBytes: 1024, Context: 8192, Metadata: meta})
	if fromFlag.KVCacheBytes != explicit.KVCacheBytes {
		t.Fatalf("dashed ctx-size option=%d want %d", fromFlag.KVCacheBytes, explicit.KVCacheBytes)
	}
	frac, cpu := gpuOffloadFraction(map[string]string{"n-gpu-layers": "bogus"}, 32)
	if frac != 1 || cpu {
		t.Fatalf("bogus layers frac=%v cpu=%v", frac, cpu)
	}
	frac, cpu = gpuOffloadFraction(map[string]string{"gpu-layers": "32"}, 32)
	if frac != 1 || cpu {
		t.Fatalf("full layers frac=%v cpu=%v", frac, cpu)
	}
}

type errDemand string

func (e errDemand) Error() string { return string(e) }
