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

func estimateGenerationSpeed(snapshot hardware.Snapshot, memory MemoryEstimate, offload Offload, guide QuantizationGuide) GenerationSpeedEstimate {
	if offload.Mode != "full" && offload.Mode != "multi_gpu" {
		if offload.Mode == "partial" || offload.Mode == "hybrid" || offload.Mode == "cpu" {
			return unavailableGenerationSpeed("Generation speed is not estimated for CPU/offloaded placements yet because system-RAM and host↔GPU bandwidth are not measured by the manager.")
		}
		return unavailableGenerationSpeed("A runnable GPU placement is required before generation speed can be estimated.")
	}
	if memory.WeightsBytes <= 0 {
		return unavailableGenerationSpeed("The GGUF weight size is unavailable, so memory traffic per generated token cannot be estimated.")
	}
	if len(offload.Devices) == 0 {
		return unavailableGenerationSpeed("The GPU placement does not identify which device will run the model.")
	}

	byID := make(map[string]hardware.GPU, len(snapshot.GPUs))
	for _, gpu := range snapshot.GPUs {
		byID[gpu.ID] = gpu
	}
	devices := make([]hardware.GPU, 0, len(offload.Devices))
	for _, id := range offload.Devices {
		gpu, ok := byID[id]
		if !ok {
			return unavailableGenerationSpeed("The selected GPU is missing from the current hardware snapshot.")
		}
		if gpu.MemoryBandwidthBytesPerSecond <= 0 {
			return unavailableGenerationSpeed("GPU memory-bandwidth telemetry is unavailable for " + id + ", so the manager will not invent a generation-speed number.")
		}
		devices = append(devices, gpu)
	}

	trafficBytes := float64(memory.WeightsBytes + memory.KVCacheBytes)
	if trafficBytes <= 0 {
		return unavailableGenerationSpeed("Estimated decode memory traffic is unavailable.")
	}
	lowEfficiency, highEfficiency := quantizationBandwidthEfficiency(guide.Name)
	fractions := tensorSplitFractions(offload.TensorSplit, len(devices))

	var slowSeconds, fastSeconds float64
	for index, gpu := range devices {
		deviceTraffic := trafficBytes * fractions[index]
		bandwidth := float64(gpu.MemoryBandwidthBytesPerSecond)
		// llama.cpp layer-split decode traverses the allocated model layers, so
		// their memory-read times are additive; bandwidth must not simply be summed.
		slowSeconds += deviceTraffic / (bandwidth * lowEfficiency)
		fastSeconds += deviceTraffic / (bandwidth * highEfficiency)
	}
	if len(devices) > 1 {
		// Cross-device synchronization and transfers are not represented by VRAM
		// bandwidth. Without topology telemetry, include a conservative allowance.
		penalty := 1 + 0.05*float64(len(devices)-1)
		if penalty > 1.20 {
			penalty = 1.20
		}
		slowSeconds *= penalty
		fastSeconds *= penalty
	}
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

	bandwidthDescription := formatDeviceBandwidth(devices)
	reason := fmt.Sprintf(
		"Bandwidth-limited generation/decode estimate using %s, %s of GGUF weights and %s of KV-cache traffic at the selected context. The range assumes %.0f–%.0f%% of theoretical VRAM bandwidth; actual llama.cpp kernels, clocks and workload can differ.",
		bandwidthDescription,
		formatBinaryBytes(memory.WeightsBytes),
		formatBinaryBytes(memory.KVCacheBytes),
		lowEfficiency*100,
		highEfficiency*100,
	)
	if len(devices) > 1 {
		reason += " Multi-GPU estimates follow the planned tensor split and include a conservative synchronization allowance."
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
