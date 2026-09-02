package scheduler

import (
	"math"
	"strconv"
	"strings"
)

const (
	demandMiB             = int64(1024 * 1024)
	defaultDemandContext  = 4096
	minRuntimeOverheadMiB = 256 * demandMiB
)

type GPUResourceDemand struct {
	DeviceID string
	Bytes    int64
}

type ResourceDemand struct {
	HostRAMBytes         int64
	WeightsBytes         int64
	KVCacheBytes         int64
	RuntimeOverheadBytes int64
	GPU                  []GPUResourceDemand
	Confidence           string
}

func (d ResourceDemand) VRAMBytes() int64 {
	total := int64(0)
	for _, gpu := range d.GPU {
		total += gpu.Bytes
	}
	return total
}

type KVMetadata struct {
	Architecture  string
	ContextLength int64
	BlockCount    int64
	Embedding     int64
	HeadCount     int64
	KVHeadCount   int64
	KeyLength     int64
	ValueLength   int64
	ExpertCount   int64
}

type DemandInput struct {
	WeightsBytes int64
	Context      int64
	Metadata     KVMetadata
	MetadataErr  error
	Options      map[string]string
}

func EstimateDemand(in DemandInput) ResourceDemand {
	weights := in.WeightsBytes
	if weights < 0 {
		weights = 0
	}
	context := in.Context
	if context <= 0 {
		context = parseContextOption(in.Options)
	}
	if context <= 0 {
		context = defaultDemandContext
	}
	overhead := int64(math.Ceil(float64(weights) * 0.05))
	if overhead < minRuntimeOverheadMiB {
		overhead = minRuntimeOverheadMiB
	}
	kv := estimateKVBytes(context, in.Metadata, in.Options)
	fraction, cpuOnly := gpuOffloadFraction(in.Options, in.Metadata.BlockCount)
	kvGPU := kvOnGPU(in.Options, cpuOnly)

	weightsGPU := int64(math.Round(float64(weights) * fraction))
	if weightsGPU < 0 {
		weightsGPU = 0
	}
	if weightsGPU > weights {
		weightsGPU = weights
	}
	if !cpuOnly {
		if cpuMoe, ok := cpuMoeBlocks(in.Options, in.Metadata.BlockCount); ok && in.Metadata.ExpertCount > 0 {
			moeGPU, _ := MoEWeightDistribution(weights, in.Metadata.BlockCount, cpuMoe, in.Metadata.ExpertCount)
			if moeGPU < weightsGPU {
				weightsGPU = moeGPU
			}
		}
	}
	kvGPUBytes, kvRAMBytes := kv, int64(0)
	if !kvGPU {
		kvGPUBytes, kvRAMBytes = 0, kv
	}
	overheadGPU, overheadRAM := overhead, int64(0)
	if cpuOnly {
		overheadGPU, overheadRAM = 0, overhead
	}

	demand := ResourceDemand{
		WeightsBytes:         weights,
		KVCacheBytes:         kv,
		RuntimeOverheadBytes: overhead,
		HostRAMBytes:         (weights - weightsGPU) + kvRAMBytes + overheadRAM,
		Confidence:           demandConfidence(in.Metadata, in.MetadataErr),
	}
	vram := weightsGPU + kvGPUBytes + overheadGPU
	if vram > 0 {
		demand.GPU = []GPUResourceDemand{{Bytes: vram}}
	}
	return demand
}

// ExpertWeightShare estimates the routed-expert share of GGUF weight bytes.
// It is intentionally conservative until tensor-directory accounting is added.
func ExpertWeightShare(expertCount int64) float64 {
	if expertCount > 1 {
		return math.Min(0.85, float64(expertCount-1)/float64(expertCount))
	}
	return 0.75
}

// MoEWeightDistribution estimates how many weight bytes remain on GPU when
// routed experts from cpuMoeBlocks transformer blocks are placed in host RAM.
func MoEWeightDistribution(weights, blockCount, cpuMoeBlocks, expertCount int64) (gpuWeights, hostWeights int64) {
	if weights <= 0 {
		return 0, 0
	}
	if blockCount <= 0 || cpuMoeBlocks <= 0 || expertCount <= 0 {
		return weights, 0
	}
	if cpuMoeBlocks > blockCount {
		cpuMoeBlocks = blockCount
	}
	share := ExpertWeightShare(expertCount)
	hostWeights = int64(math.Round(float64(weights) * share * float64(cpuMoeBlocks) / float64(blockCount)))
	if hostWeights < 0 {
		hostWeights = 0
	}
	if hostWeights > weights {
		hostWeights = weights
	}
	return weights - hostWeights, hostWeights
}

func cpuMoeBlocks(options map[string]string, blockCount int64) (int64, bool) {
	if blockCount <= 0 {
		return 0, false
	}
	if optionEnabled(options, "cpu-moe") {
		return blockCount, true
	}
	raw := strings.TrimSpace(optionValue(options, "n-cpu-moe"))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 || value > blockCount {
		return 0, false
	}
	return value, true
}

func optionEnabled(options map[string]string, key string) bool {
	switch strings.ToLower(strings.TrimSpace(optionValue(options, key))) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

func estimateKVBytes(context int64, m KVMetadata, options map[string]string) int64 {
	if context <= 0 || m.BlockCount <= 0 || m.Embedding <= 0 || m.HeadCount <= 0 {
		return 0
	}
	kvHeads := m.KVHeadCount
	if kvHeads <= 0 {
		kvHeads = m.HeadCount
	}
	headDim := m.Embedding / m.HeadCount
	keyDim, valueDim := m.KeyLength, m.ValueLength
	if keyDim <= 0 {
		keyDim = headDim
	}
	if valueDim <= 0 {
		valueDim = headDim
	}
	if headDim <= 0 || keyDim <= 0 || valueDim <= 0 {
		return 0
	}
	keyBytes := cacheElementBytes(options, "cache-type-k")
	valueBytes := cacheElementBytes(options, "cache-type-v")
	return context * m.BlockCount * kvHeads * (keyDim*keyBytes + valueDim*valueBytes)
}

func cacheElementBytes(options map[string]string, key string) int64 {
	value := strings.ToLower(strings.TrimSpace(optionValue(options, key)))
	switch value {
	case "":
		return 2
	case "f32":
		return 4
	case "f16", "bf16":
		return 2
	case "q8_0", "q8_1", "q6_k", "q5_0", "q5_1", "q5_k", "q4_0", "q4_1", "q4_k", "q3_k", "q2_k":
		return 1
	default:
		return 2
	}
}

func gpuOffloadFraction(options map[string]string, blockCount int64) (float64, bool) {
	raw := optionValue(options, "gpu-layers")
	if raw == "" {
		raw = optionValue(options, "n-gpu-layers")
	}
	if strings.TrimSpace(raw) == "" {
		return 1, false
	}
	layers, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 1, false
	}
	if layers <= 0 {
		return 0, true
	}
	if blockCount <= 0 || layers >= blockCount {
		return 1, false
	}
	return float64(layers) / float64(blockCount), false
}

func kvOnGPU(options map[string]string, cpuOnly bool) bool {
	if cpuOnly {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(optionValue(options, "no-kv-offload"))) {
	case "true", "1", "yes":
		return false
	default:
		return true
	}
}

func parseContextOption(options map[string]string) int64 {
	raw := strings.TrimSpace(optionValue(options, "ctx-size"))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func optionValue(options map[string]string, key string) string {
	if options == nil {
		return ""
	}
	if value, ok := options[key]; ok {
		return value
	}
	return options["--"+key]
}

func demandConfidence(m KVMetadata, err error) string {
	if err == nil && m.BlockCount > 0 && m.Embedding > 0 && m.HeadCount > 0 {
		return "high"
	}
	if m.Architecture != "" || m.ContextLength > 0 || m.BlockCount > 0 {
		return "medium"
	}
	return "low"
}
