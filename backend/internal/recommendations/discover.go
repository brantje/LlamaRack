package recommendations

import (
	"errors"
	"sort"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
)

const (
	FitGPU      = "gpu"
	FitMultiGPU = "multi_gpu"
	FitHybrid   = "hybrid"
	FitCPU      = "cpu"
	FitNo       = "no_fit"
	FitUnknown  = "unknown"
)

// ArtifactInput contains only provider facts. Discover recommendations are
// deliberately calculated in the backend so the browser never has to invent
// memory or placement heuristics.
type ArtifactInput struct {
	ID           string
	Quantization string
	WeightsBytes int64
	Complete     bool
}

type QuantizationGuide struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier"`
	Quality  string `json:"quality"`
	Memory   string `json:"memory"`
	Speed    string `json:"speed"`
	Summary  string `json:"summary"`
	Tradeoff string `json:"tradeoff"`
	Warning  string `json:"warning,omitempty"`
	Known    bool   `json:"known"`
	rank     int
}

type DiscoverArtifact struct {
	ArtifactID   string            `json:"artifact_id"`
	Quantization QuantizationGuide `json:"quantization"`
	Recommended bool              `json:"recommended"`
	Runnable     bool              `json:"runnable"`
	Fit          string            `json:"fit"`
	FitLabel     string            `json:"fit_label"`
	Reason       string            `json:"reason"`
	Memory       MemoryEstimate    `json:"memory"`
	Offload      Offload           `json:"offload"`
	Confidence   string            `json:"confidence"`
	Warnings     []string          `json:"warnings,omitempty"`
	weightsBytes int64
	complete     bool
}

type DiscoverAnalysis struct {
	ContextLength              int64              `json:"context_length"`
	ContextCapability          int64              `json:"context_capability"`
	ContextAssumed             bool               `json:"context_assumed"`
	Metadata                   Metadata            `json:"metadata"`
	MetadataWarning            string              `json:"metadata_warning,omitempty"`
	HardwareWarning            string              `json:"hardware_warning,omitempty"`
	HardwareAvailable          bool                `json:"hardware_available"`
	HybridRecommendations      bool                `json:"hybrid_recommendations_enabled"`
	Artifacts                  []DiscoverArtifact  `json:"artifacts"`
}

// AnalyzeDiscover evaluates remote GGUF artifacts using the same memory model
// and scheduler-backed placement logic used by local-model recommendations.
func AnalyzeDiscover(inputs []ArtifactInput, metadata Metadata, metadataErr error, snapshot hardware.Snapshot, requestedContext int64, hardwareErr error, allowHybrid bool) DiscoverAnalysis {
	contextLength, assumed := chooseContext(requestedContext)
	result := DiscoverAnalysis{
		ContextLength:         contextLength,
		ContextCapability:     metadata.ContextLength,
		ContextAssumed:        assumed,
		Metadata:              metadata,
		HybridRecommendations: allowHybrid,
		HardwareAvailable:     hardwareTelemetryAvailable(snapshot, hardwareErr),
		Artifacts:             make([]DiscoverArtifact, 0, len(inputs)),
	}
	if metadataErr != nil {
		result.MetadataWarning = metadataErr.Error()
	}
	if hardwareErr != nil {
		result.HardwareWarning = hardwareErr.Error()
	}

	metadataReady := discoverMetadataReady(metadata)
	for _, input := range inputs {
		guide := ClassifyQuantization(input.Quantization)
		artifact := DiscoverArtifact{
			ArtifactID: input.ID,
			Quantization: guide,
			Fit: FitUnknown,
			FitLabel: "Fit unknown",
			Confidence: confidence(metadata, metadataErr),
			weightsBytes: input.WeightsBytes,
			complete: input.Complete,
		}
		if guide.Warning != "" {
			artifact.Warnings = append(artifact.Warnings, guide.Warning)
		}
		switch {
		case !input.Complete:
			artifact.Reason = "This split artifact is incomplete, so the manager cannot make a runnable recommendation for it."
		case !metadataReady:
			artifact.Reason = "The GGUF does not expose enough architecture metadata for a context-aware KV-cache estimate."
			artifact.Warnings = append(artifact.Warnings, "Hardware fit stays unknown until enough GGUF metadata is available.")
		case !result.HardwareAvailable:
			artifact.Reason = "Hardware telemetry is unavailable, so this choice is shown without a device-specific fit claim."
		case metadata.ContextLength > 0 && contextLength > metadata.ContextLength:
			artifact.Fit = FitNo
			artifact.FitLabel = "Doesn't fit"
			artifact.Reason = "The selected context is larger than the context capability reported by the GGUF metadata."
		case input.WeightsBytes <= 0:
			artifact.Reason = "The artifact size is unavailable, so a reliable memory estimate cannot be produced."
		default:
			artifact.Memory = estimateMemory(input.WeightsBytes, contextLength, metadata)
			artifact.Runnable, artifact.Offload = discoverOffload(snapshot, artifact.Memory, metadata)
			artifact.Fit, artifact.FitLabel = discoverFit(artifact.Runnable, artifact.Offload)
			artifact.Reason = artifact.Offload.Reason
		}
		result.Artifacts = append(result.Artifacts, artifact)
	}

	markDiscoverRecommendation(result.Artifacts, allowHybrid)
	sort.SliceStable(result.Artifacts, func(i, j int) bool {
		a, b := result.Artifacts[i], result.Artifacts[j]
		if a.Recommended != b.Recommended {
			return a.Recommended
		}
		if a.Quantization.rank != b.Quantization.rank {
			return a.Quantization.rank > b.Quantization.rank
		}
		if a.Quantization.Known != b.Quantization.Known {
			return a.Quantization.Known
		}
		if a.weightsBytes != b.weightsBytes && a.weightsBytes > 0 && b.weightsBytes > 0 {
			return a.weightsBytes < b.weightsBytes
		}
		return a.ArtifactID < b.ArtifactID
	})
	return result
}

func discoverOffload(snapshot hardware.Snapshot, memory MemoryEstimate, metadata Metadata) (bool, Offload) {
	if len(snapshot.GPUs) > 0 {
		return recommendOffload(snapshot, memory, metadata)
	}
	if fitsRAM(snapshot.RAMAvailableBytes, memory.CPUOnlyRAMBytes) {
		return true, Offload{Mode: "cpu", Reason: "No GPU was detected; the model, selected context and conservative runtime headroom fit in currently available system RAM."}
	}
	return false, Offload{Mode: "cpu", Reason: "No GPU was detected and available system RAM is below the conservative estimate for this model and context."}
}

func discoverFit(runnable bool, offload Offload) (string, string) {
	if !runnable {
		return FitNo, "Doesn't fit"
	}
	switch offload.Mode {
	case "full":
		return FitGPU, "Fits on GPU"
	case "multi_gpu":
		return FitMultiGPU, "Fits across GPUs"
	case "partial", "hybrid":
		return FitHybrid, "GPU + CPU"
	case "cpu":
		return FitCPU, "CPU only"
	default:
		return FitUnknown, "Fit unknown"
	}
}

func discoverMetadataReady(metadata Metadata) bool {
	return metadata.BlockCount > 0 && metadata.Embedding > 0 && metadata.HeadCount > 0
}

func hardwareTelemetryAvailable(snapshot hardware.Snapshot, err error) bool {
	if len(snapshot.GPUs) > 0 || snapshot.RAMAvailableBytes > 0 || snapshot.RAMTotalBytes > 0 {
		return true
	}
	return err == nil && !errors.Is(err, contextUnavailableError{})
}

// contextUnavailableError is intentionally unexported; it keeps the availability
// predicate explicit without treating a machine with zeroed fake test data as a
// successfully detected hardware snapshot.
type contextUnavailableError struct{}
func (contextUnavailableError) Error() string { return "hardware telemetry unavailable" }

func markDiscoverRecommendation(artifacts []DiscoverArtifact, allowHybrid bool) {
	candidates := make([]int, 0, len(artifacts))
	gpuCandidates := make([]int, 0, len(artifacts))
	for index := range artifacts {
		artifact := artifacts[index]
		if !artifact.complete || !artifact.Runnable || !artifact.Quantization.Known {
			continue
		}
		candidates = append(candidates, index)
		if artifact.Fit == FitGPU || artifact.Fit == FitMultiGPU {
			gpuCandidates = append(gpuCandidates, index)
		}
	}
	if !allowHybrid && len(gpuCandidates) > 0 {
		candidates = gpuCandidates
	}
	if len(candidates) == 0 {
		return
	}
	best := candidates[0]
	for _, index := range candidates[1:] {
		if artifacts[index].Quantization.rank > artifacts[best].Quantization.rank {
			best = index
			continue
		}
		if artifacts[index].Quantization.rank == artifacts[best].Quantization.rank && artifacts[index].weightsBytes < artifacts[best].weightsBytes {
			best = index
		}
	}
	artifacts[best].Recommended = true
}

func ClassifyQuantization(value string) QuantizationGuide {
	q := strings.ToUpper(strings.TrimSpace(value))
	base := ExplainQuantization(q)
	guide := QuantizationGuide{
		Name: q,
		Tier: "Unknown profile",
		Quality: "Unknown",
		Memory: "Unknown",
		Speed: "Hardware-dependent",
		Summary: base.Summary,
		Tradeoff: base.Tradeoff,
	}
	prefix := q
	if strings.HasPrefix(prefix, "IQ") {
		prefix = "Q" + strings.TrimPrefix(prefix, "IQ")
	}
	switch {
	case strings.HasPrefix(prefix, "Q2"):
		guide.Tier, guide.Quality, guide.Memory, guide.Speed, guide.Known, guide.rank = "Very compact", "Very low", "Very low", "Often lighter to load", true, 20
		guide.Warning = "Q2 variants make significant quality trade-offs. Prefer Q4 or better when memory allows."
	case strings.HasPrefix(prefix, "Q3"):
		guide.Tier, guide.Quality, guide.Memory, guide.Speed, guide.Known, guide.rank = "Compact", "Low", "Low", "Often lighter to load", true, 30
		guide.Warning = "Q3 variants make noticeable quality trade-offs. Prefer Q4 or better when memory allows."
	case strings.HasPrefix(prefix, "Q4"):
		guide.Tier, guide.Quality, guide.Memory, guide.Speed, guide.Known, guide.rank = "Balanced", "Balanced", "Moderate", "Good general-purpose balance", true, 40
	case strings.HasPrefix(prefix, "Q5"):
		guide.Tier, guide.Quality, guide.Memory, guide.Speed, guide.Known, guide.rank = "High quality", "High", "Moderate-high", "Hardware-dependent", true, 50
	case strings.HasPrefix(prefix, "Q6"):
		guide.Tier, guide.Quality, guide.Memory, guide.Speed, guide.Known, guide.rank = "High quality", "High", "High", "Hardware-dependent", true, 60
	case strings.HasPrefix(prefix, "Q8"):
		guide.Tier, guide.Quality, guide.Memory, guide.Speed, guide.Known, guide.rank = "Maximum quality", "Maximum", "Very high", "Hardware-dependent", true, 80
		guide.Warning = "Q8 usually offers a small quality gain over Q6 for substantially more memory."
	case q == "F16", q == "BF16":
		guide.Tier, guide.Quality, guide.Memory, guide.Speed, guide.Known, guide.rank = "Maximum quality", "Maximum", "Extreme", "Hardware-dependent", true, 90
		guide.Warning = "Near-full precision is usually unnecessary for local inference and leaves much less memory for context."
	case q == "F32":
		guide.Tier, guide.Quality, guide.Memory, guide.Speed, guide.Known, guide.rank = "Maximum quality", "Maximum", "Extreme", "Hardware-dependent", true, 100
		guide.Warning = "Full precision is rarely practical for local inference; quantized variants normally retain useful quality with far lower memory use."
	}
	return guide
}
