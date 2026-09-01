package recommendations

import (
	"math"
	"strings"

	"github.com/brantje/llamarack/backend/internal/ggufmeta"
	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

const (
	mib                = int64(1024 * 1024)
	defaultContext     = 4096
	defaultVRAMReserve = 512 * mib
	defaultRAMReserve  = 1024 * mib
)

type Metadata struct {
	Architecture  string `json:"architecture,omitempty"`
	ContextLength int64  `json:"context_length,omitempty"`
	BlockCount    int64  `json:"block_count,omitempty"`
	Embedding     int64  `json:"embedding_length,omitempty"`
	HeadCount     int64  `json:"head_count,omitempty"`
	KVHeadCount   int64  `json:"kv_head_count,omitempty"`
	KeyLength     int64  `json:"key_length,omitempty"`
	ValueLength   int64  `json:"value_length,omitempty"`
}

type QuantizationInfo struct {
	Name     string `json:"name,omitempty"`
	Summary  string `json:"summary"`
	Tradeoff string `json:"tradeoff"`
}

type MemoryEstimate struct {
	WeightsBytes         int64 `json:"weights_bytes"`
	KVCacheBytes         int64 `json:"kv_cache_bytes"`
	RuntimeOverheadBytes int64 `json:"runtime_overhead_bytes"`
	CPUOnlyRAMBytes      int64 `json:"cpu_only_ram_bytes"`
	FullOffloadVRAMBytes int64 `json:"full_offload_vram_bytes"`
}

type Offload struct {
	Mode        string   `json:"mode"`
	GPULayers   int64    `json:"gpu_layers,omitempty"`
	Devices     []string `json:"devices,omitempty"`
	TensorSplit string   `json:"tensor_split,omitempty"`
	KVOnGPU     bool     `json:"kv_on_gpu"`
	Reason      string   `json:"reason"`
}

type Recommendation struct {
	ModelID           string           `json:"model_id"`
	ContextLength     int64            `json:"context_length"`
	ContextCapability int64            `json:"context_capability"`
	ContextAssumed    bool             `json:"context_assumed"`
	Confidence        string           `json:"confidence"`
	Metadata          Metadata         `json:"metadata"`
	MetadataWarning   string           `json:"metadata_warning,omitempty"`
	HardwareWarning   string           `json:"hardware_warning,omitempty"`
	Quantization      QuantizationInfo `json:"quantization"`
	Memory            MemoryEstimate   `json:"memory"`
	CurrentFit        bool             `json:"current_fit"`
	TotalHardwareFit  bool             `json:"total_hardware_fit"`
	CPUFit            bool             `json:"cpu_fit"`
	Offload           Offload          `json:"offload"`
}

func Analyze(model models.Model, path string, snapshot hardware.Snapshot, requestedContext int64, hardwareErr error) Recommendation {
	metadata, metadataErr := ReadMetadata(path)
	contextLength, assumed := chooseContext(requestedContext)
	capability := int64(model.ContextLength)
	if capability <= 0 {
		capability = metadata.ContextLength
	}
	memory := estimateMemory(model.TotalBytes, contextLength, metadata)
	result := Recommendation{
		ModelID: model.ID, ContextLength: contextLength, ContextCapability: capability, ContextAssumed: assumed,
		Metadata: metadata, Quantization: ExplainQuantization(model.Quantization), Memory: memory,
	}
	if metadataErr != nil {
		result.MetadataWarning = metadataErr.Error()
	}
	if hardwareErr != nil {
		result.HardwareWarning = hardwareErr.Error()
	}
	result.Confidence = confidence(metadata, metadataErr)
	result.CPUFit = fitsRAM(snapshot.RAMAvailableBytes, memory.CPUOnlyRAMBytes)
	result.CurrentFit, result.Offload = recommendOffload(snapshot, memory, metadata)
	total := assumeIdleSnapshot(snapshot)
	if len(total.GPUs) > 0 {
		result.TotalHardwareFit, _ = recommendOffload(total, memory, metadata)
	} else {
		result.TotalHardwareFit = fitsRAM(snapshot.RAMTotalBytes, memory.CPUOnlyRAMBytes)
	}
	if len(snapshot.GPUs) == 0 {
		result.CurrentFit = result.CPUFit
		if result.CPUFit {
			result.Offload = Offload{Mode: "cpu", Reason: "No GPU was detected; the estimated model and KV cache fit in currently available system RAM."}
		} else {
			result.Offload = Offload{Mode: "cpu", Reason: "No GPU was detected and currently available system RAM is below the conservative estimate."}
		}
	}
	return result
}

// assumeIdleSnapshot returns a copy of snapshot with free VRAM/RAM treated as
// installed capacity. Only overwrites free/available when totals are known so
// test fixtures that set FreeBytes alone keep working.
func assumeIdleSnapshot(snapshot hardware.Snapshot) hardware.Snapshot {
	idle := snapshot
	if idle.RAMTotalBytes > 0 {
		idle.RAMAvailableBytes = idle.RAMTotalBytes
	}
	if len(idle.GPUs) == 0 {
		return idle
	}
	idle.GPUs = append([]hardware.GPU(nil), idle.GPUs...)
	for i := range idle.GPUs {
		if idle.GPUs[i].TotalBytes > 0 {
			idle.GPUs[i].FreeBytes = idle.GPUs[i].TotalBytes
		}
	}
	return idle
}

func chooseContext(requested int64) (int64, bool) {
	if requested > 0 {
		return requested, false
	}
	return defaultContext, true
}

func estimateMemory(weights, context int64, metadata Metadata) MemoryEstimate {
	if weights < 0 {
		weights = 0
	}
	overhead := int64(math.Ceil(float64(weights) * 0.05))
	if overhead < 256*mib {
		overhead = 256 * mib
	}
	kv := estimateKV(context, metadata)
	return MemoryEstimate{
		WeightsBytes: weights, KVCacheBytes: kv, RuntimeOverheadBytes: overhead,
		CPUOnlyRAMBytes: weights + kv + overhead, FullOffloadVRAMBytes: weights + kv + overhead,
	}
}

func estimateKV(context int64, m Metadata) int64 {
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
	return context * m.BlockCount * kvHeads * (keyDim + valueDim) * 2
}

func recommendOffload(snapshot hardware.Snapshot, memory MemoryEstimate, metadata Metadata) (bool, Offload) {
	if len(snapshot.GPUs) == 0 {
		return false, Offload{}
	}
	placement, _ := scheduler.PlanPlacement(snapshot, scheduler.PlacementRequest{RequiredBytes: memory.FullOffloadVRAMBytes})
	if placement.Fits {
		mode := "full"
		reason := "The full model, estimated KV cache and runtime headroom fit with the scheduler VRAM reserve."
		if len(placement.Devices) > 1 {
			mode = "multi_gpu"
			reason = "No single GPU fits the full estimate, but the scheduler can place it across the minimum practical GPU set."
		}
		return true, Offload{Mode: mode, GPULayers: metadata.BlockCount, Devices: placement.Devices, TensorSplit: placement.TensorSplit, KVOnGPU: true, Reason: reason}
	}

	bestID := ""
	var best int64
	for _, gpu := range snapshot.GPUs {
		usable := gpu.FreeBytes - defaultVRAMReserve
		if usable > best {
			best, bestID = usable, gpu.ID
		}
	}
	if best <= 0 || memory.WeightsBytes <= 0 {
		if fitsRAM(snapshot.RAMAvailableBytes, memory.CPUOnlyRAMBytes) {
			return true, Offload{Mode: "cpu", Reason: "GPU headroom is too small for useful offload, but the estimate fits in currently available system RAM."}
		}
		return false, Offload{Mode: "cpu", Reason: "Current GPU headroom is too small for useful offload and available system RAM is below the conservative estimate."}
	}

	fixedGPU := memory.KVCacheBytes + memory.RuntimeOverheadBytes
	if best > fixedGPU {
		fraction := math.Min(1, float64(best-fixedGPU)/float64(memory.WeightsBytes))
		if fraction > 0 {
			offloadedWeights := int64(float64(memory.WeightsBytes) * fraction)
			cpuNeeded := memory.WeightsBytes - offloadedWeights
			if fitsRAM(snapshot.RAMAvailableBytes, cpuNeeded) {
				layers := recommendedLayers(metadata.BlockCount, fraction)
				return true, Offload{Mode: "partial", GPULayers: layers, Devices: []string{bestID}, KVOnGPU: true, Reason: "Full offload does not fit currently; keep the KV cache on GPU and offload the largest useful share of model layers to the best single GPU."}
			}
		}
	}

	if best > memory.RuntimeOverheadBytes {
		fraction := math.Min(1, float64(best-memory.RuntimeOverheadBytes)/float64(memory.WeightsBytes))
		if fraction > 0 {
			offloadedWeights := int64(float64(memory.WeightsBytes) * fraction)
			cpuNeeded := memory.KVCacheBytes + (memory.WeightsBytes - offloadedWeights)
			if fitsRAM(snapshot.RAMAvailableBytes, cpuNeeded) {
				layers := recommendedLayers(metadata.BlockCount, fraction)
				return true, Offload{Mode: "hybrid", GPULayers: layers, Devices: []string{bestID}, KVOnGPU: false, Reason: "The selected context makes an all-GPU KV cache too large. Keep KV in system RAM and use the available GPU for model-layer offload instead of falling back to CPU-only loading."}
			}
		}
	}

	if fitsRAM(snapshot.RAMAvailableBytes, memory.CPUOnlyRAMBytes) {
		return true, Offload{Mode: "cpu", Reason: "The current GPU/RAM combination cannot satisfy a conservative offload plan, but CPU-only loading fits available system RAM."}
	}
	return false, Offload{Mode: "cpu", Reason: "Current free GPU and system memory are below the conservative estimate for this context size."}
}

func recommendedLayers(blockCount int64, fraction float64) int64 {
	if blockCount <= 0 || fraction <= 0 {
		return 0
	}
	layers := int64(math.Floor(float64(blockCount) * fraction))
	if layers < 1 {
		return 1
	}
	if layers > blockCount {
		return blockCount
	}
	return layers
}

func fitsRAM(available, required int64) bool {
	return available > defaultRAMReserve && available-defaultRAMReserve >= required
}

func confidence(m Metadata, err error) string {
	if err == nil && m.BlockCount > 0 && m.Embedding > 0 && m.HeadCount > 0 {
		return "high"
	}
	if m.Architecture != "" || m.ContextLength > 0 || m.BlockCount > 0 {
		return "medium"
	}
	return "low"
}

func ExplainQuantization(value string) QuantizationInfo {
	q := strings.ToUpper(strings.TrimSpace(value))
	info := QuantizationInfo{Name: q, Summary: "Quantization is unknown; memory estimates use the actual GGUF file size.", Tradeoff: "Quality and speed vary by architecture and hardware."}
	switch {
	case strings.HasPrefix(q, "Q2"), strings.HasPrefix(q, "Q3"):
		info.Summary = "Very compact quantization aimed at minimizing RAM/VRAM use."
		info.Tradeoff = "Usually gives up more model quality than Q4+ variants in exchange for fitting smaller hardware."
	case strings.HasPrefix(q, "Q4"):
		info.Summary = "Balanced quantization with a relatively small memory footprint."
		info.Tradeoff = "A common general-purpose balance between memory use and retained model quality."
	case strings.HasPrefix(q, "Q5"):
		info.Summary = "Higher-fidelity quantization with moderate additional memory use."
		info.Tradeoff = "Often useful when Q4 fits comfortably and extra memory can be spent on quality."
	case strings.HasPrefix(q, "Q6"):
		info.Summary = "High-fidelity quantization with a larger memory footprint."
		info.Tradeoff = "Retains more precision but leaves less VRAM for context and concurrent models."
	case strings.HasPrefix(q, "Q8"):
		info.Summary = "Large quantization close to full-precision behavior for many use cases."
		info.Tradeoff = "Uses substantially more memory than Q4/Q5 variants and is rarely the best fit-first choice."
	case q == "F16", q == "BF16", q == "F32":
		info.Summary = "Full or near-full precision weights with a very large memory footprint."
		info.Tradeoff = "Best suited to hardware with abundant memory; quantized variants are usually easier to deploy."
	}
	return info
}

func ReadMetadata(path string) (Metadata, error) {
	inspection, err := ggufmeta.Inspect(path)
	if err != nil {
		return Metadata{}, err
	}
	d := inspection.Derived
	return Metadata{
		Architecture: d.Architecture, ContextLength: d.ContextLength, BlockCount: d.BlockCount,
		Embedding: d.Embedding, HeadCount: d.HeadCount, KVHeadCount: d.KVHeadCount,
		KeyLength: d.KeyLength, ValueLength: d.ValueLength,
	}, nil
}
