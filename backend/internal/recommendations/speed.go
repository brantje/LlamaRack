package recommendations

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
)

// GenerationSpeedEstimate is a bandwidth-model estimate for autoregressive
// decode. It deliberately does not claim to estimate prompt/prefill speed,
// which is substantially more compute- and batch-dependent.
type GenerationSpeedEstimate struct {
	Estimated          bool    `json:"estimated"`
	MinTokensPerSecond float64 `json:"min_tokens_per_second,omitempty"`
	MaxTokensPerSecond float64 `json:"max_tokens_per_second,omitempty"`
	Label              string  `json:"label"`
	Reason             string  `json:"reason"`
}

func unavailableGenerationSpeed(reason string) GenerationSpeedEstimate {
	return GenerationSpeedEstimate{Label: "Estimate unavailable", Reason: reason}
}

func estimateGenerationSpeed(snapshot hardware.Snapshot, memory MemoryEstimate, offload Offload, guide QuantizationGuide, metadata Metadata) GenerationSpeedEstimate {
	if memory.WeightsBytes <= 0 {
		return unavailableGenerationSpeed("The GGUF weight size is unavailable, so memory traffic per generated token cannot be estimated.")
	}

	switch offload.Mode {
	case "full", "multi_gpu":
		return estimateGPUGenerationSpeed(snapshot, memory, offload, guide)
	case "partial", "hybrid":
		return estimateHybridGenerationSpeed(snapshot, memory, offload, guide, metadata)
	case "cpu":
		return unavailableGenerationSpeed("CPU-only generation speed is not estimated yet; the current model focuses on GPU and GPU + CPU placements.")
	default:
		return unavailableGenerationSpeed("A runnable GPU placement is required before generation speed can be estimated.")
	}
}

func estimateGPUGenerationSpeed(snapshot hardware.Snapshot, memory MemoryEstimate, offload Offload, guide QuantizationGuide) GenerationSpeedEstimate {
	devices, reason := speedDevices(snapshot, offload, false)
	if reason != "" {
		return unavailableGenerationSpeed(reason)
	}
	trafficBytes := float64(memory.WeightsBytes + memory.KVCacheBytes)
	if trafficBytes <= 0 {
		return unavailableGenerationSpeed("Estimated decode memory traffic is unavailable.")
	}
	lowEfficiency, highEfficiency := quantizationBandwidthEfficiency(guide.Name)
	slowSeconds, fastSeconds := gpuTrafficSeconds(devices, offload.TensorSplit, trafficBytes, lowEfficiency, highEfficiency)
	if len(devices) > 1 {
		penalty := multiGPUPenalty(len(devices))
		slowSeconds *= penalty
		fastSeconds *= penalty
	}

	reason = fmt.Sprintf(
		"Bandwidth-limited generation/decode estimate using %s, %s of GGUF weights and %s of KV-cache traffic at the selected context. The range assumes %.0f–%.0f%% of theoretical VRAM bandwidth; actual llama.cpp kernels, clocks and workload can differ.",
		formatDeviceBandwidth(devices),
		formatBinaryBytes(memory.WeightsBytes),
		formatBinaryBytes(memory.KVCacheBytes),
		lowEfficiency*100,
		highEfficiency*100,
	)
	if len(devices) > 1 {
		reason += " Multi-GPU estimates follow the planned tensor split and include a conservative synchronization allowance."
	}
	return finishGenerationSpeed(slowSeconds, fastSeconds, reason)
}

func estimateHybridGenerationSpeed(snapshot hardware.Snapshot, memory MemoryEstimate, offload Offload, guide QuantizationGuide, metadata Metadata) GenerationSpeedEstimate {
	if snapshot.RAMBandwidthBytesPerSecond <= 0 {
		return unavailableGenerationSpeed("Measured host-memory bandwidth is unavailable, so GPU + CPU generation speed cannot be estimated without guessing RAM performance.")
	}
	if metadata.BlockCount <= 0 || metadata.Embedding <= 0 || offload.GPULayers <= 0 {
		return unavailableGenerationSpeed("GGUF layer/embedding metadata is incomplete, so the GPU/CPU traffic split cannot be estimated.")
	}
	devices, reason := speedDevices(snapshot, offload, true)
	if reason != "" {
		return unavailableGenerationSpeed(reason)
	}

	gpuFraction := math.Min(1, float64(offload.GPULayers)/float64(metadata.BlockCount))
	if gpuFraction <= 0 {
		return unavailableGenerationSpeed("The placement does not offload enough model layers to estimate a GPU + CPU generation path.")
	}
	gpuWeights := float64(memory.WeightsBytes) * gpuFraction
	hostWeights := float64(memory.WeightsBytes) - gpuWeights
	gpuTraffic := gpuWeights
	hostTraffic := hostWeights
	if offload.KVOnGPU {
		gpuTraffic += float64(memory.KVCacheBytes)
	} else {
		hostTraffic += float64(memory.KVCacheBytes)
	}

	gpuLow, gpuHigh := quantizationBandwidthEfficiency(guide.Name)
	gpuSlow, gpuFast := gpuTrafficSeconds(devices, offload.TensorSplit, gpuTraffic, gpuLow, gpuHigh)
	if len(devices) > 1 {
		penalty := multiGPUPenalty(len(devices))
		gpuSlow *= penalty
		gpuFast *= penalty
	}

	// The host-memory probe is already an effective copy-throughput measurement,
	// but quantized decode is less regular than memcpy and also pays dequant/kernel
	// overhead. Keep the range deliberately conservative.
	const hostLowEfficiency = 0.45
	const hostHighEfficiency = 0.70
	hostBandwidth := float64(snapshot.RAMBandwidthBytesPerSecond)
	hostSlow := hostTraffic / (hostBandwidth * hostLowEfficiency)
	hostFast := hostTraffic / (hostBandwidth * hostHighEfficiency)

	transferBytes := estimatedHybridBoundaryTraffic(metadata)
	if transferBytes <= 0 {
		return unavailableGenerationSpeed("CPU↔GPU activation traffic cannot be estimated from the available GGUF metadata.")
	}
	pcieBandwidth := slowestPCIeBandwidth(devices)
	if pcieBandwidth <= 0 {
		return unavailableGenerationSpeed("PCIe link-bandwidth telemetry is unavailable for the selected GPU, so GPU + CPU generation speed cannot be estimated without guessing host↔GPU transfer performance.")
	}
	const pcieLowEfficiency = 0.60
	const pcieHighEfficiency = 0.85
	pcieSlow := float64(transferBytes) / (float64(pcieBandwidth) * pcieLowEfficiency)
	pcieFast := float64(transferBytes) / (float64(pcieBandwidth) * pcieHighEfficiency)

	slowSeconds := gpuSlow + hostSlow + pcieSlow
	fastSeconds := gpuFast + hostFast + pcieFast
	kvLocation := "VRAM"
	if !offload.KVOnGPU {
		kvLocation = "system RAM"
	}
	reason = fmt.Sprintf(
		"Hybrid bandwidth-limited generation/decode estimate: about %.0f%% of model-layer weights are on GPU and %.0f%% remain in system RAM. GPU traffic uses %s; host traffic uses %.0f GB/s measured memory-copy throughput; approximately %s/token of activation traffic crosses a %.1f GB/s theoretical PCIe link. The selected context's %s KV cache is accounted for in %s traffic. The range assumes %.0f–%.0f%% VRAM, %.0f–%.0f%% host-memory and %.0f–%.0f%% PCIe efficiency; actual llama.cpp kernels and layer boundaries can differ.",
		gpuFraction*100,
		(1-gpuFraction)*100,
		formatDeviceBandwidth(devices),
		float64(snapshot.RAMBandwidthBytesPerSecond)/1_000_000_000,
		formatBinaryBytes(transferBytes),
		float64(pcieBandwidth)/1_000_000_000,
		formatBinaryBytes(memory.KVCacheBytes),
		kvLocation,
		gpuLow*100,
		gpuHigh*100,
		hostLowEfficiency*100,
		hostHighEfficiency*100,
		pcieLowEfficiency*100,
		pcieHighEfficiency*100,
	)
	return finishGenerationSpeed(slowSeconds, fastSeconds, reason)
}

func speedDevices(snapshot hardware.Snapshot, offload Offload, requirePCIe bool) ([]hardware.GPU, string) {
	if len(offload.Devices) == 0 {
		return nil, "The GPU placement does not identify which device will run the model."
	}
	byID := make(map[string]hardware.GPU, len(snapshot.GPUs))
	for _, gpu := range snapshot.GPUs {
		byID[gpu.ID] = gpu
	}
	devices := make([]hardware.GPU, 0, len(offload.Devices))
	for _, id := range offload.Devices {
		gpu, ok := byID[id]
		if !ok {
			return nil, "The selected GPU is missing from the current hardware snapshot."
		}
		if gpu.MemoryBandwidthBytesPerSecond <= 0 {
			return nil, "GPU memory-bandwidth telemetry is unavailable for " + id + ", so the manager will not invent a generation-speed number."
		}
		if requirePCIe && gpu.PCIeBandwidthBytesPerSecond <= 0 {
			return nil, "PCIe link-bandwidth telemetry is unavailable for " + id + ", so GPU + CPU generation speed cannot be estimated without guessing host↔GPU transfer performance."
		}
		devices = append(devices, gpu)
	}
	return devices, ""
}

func gpuTrafficSeconds(devices []hardware.GPU, tensorSplit string, trafficBytes, lowEfficiency, highEfficiency float64) (float64, float64) {
	fractions := tensorSplitFractions(tensorSplit, len(devices))
	var slowSeconds, fastSeconds float64
	for index, gpu := range devices {
		deviceTraffic := trafficBytes * fractions[index]
		bandwidth := float64(gpu.MemoryBandwidthBytesPerSecond)
		// llama.cpp layer-split decode traverses the allocated model layers, so
		// their memory-read times are additive; bandwidth must not simply be summed.
		slowSeconds += deviceTraffic / (bandwidth * lowEfficiency)
		fastSeconds += deviceTraffic / (bandwidth * highEfficiency)
	}
	return slowSeconds, fastSeconds
}

func multiGPUPenalty(deviceCount int) float64 {
	if deviceCount <= 1 {
		return 1
	}
	penalty := 1 + 0.05*float64(deviceCount-1)
	if penalty > 1.20 {
		return 1.20
	}
	return penalty
}

func estimatedHybridBoundaryTraffic(metadata Metadata) int64 {
	if metadata.Embedding <= 0 || metadata.Embedding > math.MaxInt64/8 {
		return 0
	}
	// Decode is batch=1 here. Approximate two CPU↔GPU layer-boundary crossings
	// using FP32-sized activations. Weight and KV traffic dominate, but including
	// this term lets PCIe topology affect hybrid estimates rather than disappear.
	return metadata.Embedding * 4 * 2
}

func slowestPCIeBandwidth(devices []hardware.GPU) int64 {
	var slowest int64
	for _, gpu := range devices {
		bandwidth := gpu.PCIeBandwidthBytesPerSecond
		if bandwidth <= 0 {
			return 0
		}
		if slowest == 0 || bandwidth < slowest {
			slowest = bandwidth
		}
	}
	return slowest
}

func finishGenerationSpeed(slowSeconds, fastSeconds float64, reason string) GenerationSpeedEstimate {
	if slowSeconds <= 0 || fastSeconds <= 0 {
		return unavailableGenerationSpeed("The bandwidth model could not produce a finite decode estimate.")
	}
	minTPS := roundTPS(1 / slowSeconds)
	maxTPS := roundTPS(1 / fastSeconds)
	if minTPS <= 0 || maxTPS <= 0 || math.IsInf(minTPS, 0) || math.IsInf(maxTPS, 0) || math.IsNaN(minTPS) || math.IsNaN(maxTPS) {
		return unavailableGenerationSpeed("The bandwidth model could not produce a finite decode estimate.")
	}
	if maxTPS < minTPS {
		minTPS, maxTPS = maxTPS, minTPS
	}
	return GenerationSpeedEstimate{
		Estimated:          true,
		MinTokensPerSecond: minTPS,
		MaxTokensPerSecond: maxTPS,
		Label:              formatTPSRange(minTPS, maxTPS),
		Reason:             reason,
	}
}

func quantizationBandwidthEfficiency(value string) (float64, float64) {
	q := strings.ToUpper(strings.TrimSpace(value))
	if _, ok := parseBPWQuantization(q); ok {
		return 0.48, 0.73
	}
	prefix := q
	if strings.HasPrefix(prefix, "IQ") {
		prefix = "Q" + strings.TrimPrefix(prefix, "IQ")
	}
	switch {
	case strings.HasPrefix(prefix, "Q2"), strings.HasPrefix(prefix, "Q3"):
		return 0.42, 0.66
	case strings.HasPrefix(prefix, "Q4"):
		return 0.50, 0.75
	case strings.HasPrefix(prefix, "Q5"), strings.HasPrefix(prefix, "Q6"):
		return 0.52, 0.77
	case strings.HasPrefix(prefix, "Q8"):
		return 0.55, 0.80
	case q == "F16", q == "BF16", q == "F32":
		return 0.58, 0.82
	default:
		return 0.45, 0.70
	}
}

func tensorSplitFractions(value string, deviceCount int) []float64 {
	if deviceCount <= 0 {
		return nil
	}
	parts := strings.Split(strings.TrimSpace(value), ",")
	if len(parts) != deviceCount {
		return equalFractions(deviceCount)
	}
	values := make([]float64, deviceCount)
	var total float64
	for i, part := range parts {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || parsed <= 0 {
			return equalFractions(deviceCount)
		}
		values[i] = parsed
		total += parsed
	}
	if total <= 0 {
		return equalFractions(deviceCount)
	}
	for i := range values {
		values[i] /= total
	}
	return values
}

func equalFractions(count int) []float64 {
	if count <= 0 {
		return nil
	}
	values := make([]float64, count)
	fraction := 1 / float64(count)
	for i := range values {
		values[i] = fraction
	}
	return values
}

func roundTPS(value float64) float64 {
	if value >= 10 {
		return math.Round(value)
	}
	return math.Round(value*10) / 10
}

func formatTPSRange(minTPS, maxTPS float64) string {
	if maxTPS < 0.1 {
		return "<0.1 tok/s estimated"
	}
	format := func(value float64) string {
		if value >= 10 {
			return strconv.FormatFloat(value, 'f', 0, 64)
		}
		return strconv.FormatFloat(value, 'f', 1, 64)
	}
	return "~" + format(minTPS) + "–" + format(maxTPS) + " tok/s"
}

func formatDeviceBandwidth(devices []hardware.GPU) string {
	parts := make([]string, 0, len(devices))
	for _, gpu := range devices {
		gbps := float64(gpu.MemoryBandwidthBytesPerSecond) / 1_000_000_000
		parts = append(parts, fmt.Sprintf("%s %.0f GB/s theoretical VRAM bandwidth", gpu.ID, gbps))
	}
	return strings.Join(parts, ", ")
}

func formatBinaryBytes(value int64) string {
	if value <= 0 {
		return "0 B"
	}
	const gib = float64(1024 * 1024 * 1024)
	const mib = float64(1024 * 1024)
	if float64(value) >= gib {
		return strconv.FormatFloat(float64(value)/gib, 'f', 1, 64) + " GiB"
	}
	return strconv.FormatFloat(float64(value)/mib, 'f', 0, 64) + " MiB"
}
