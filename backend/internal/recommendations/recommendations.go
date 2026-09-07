package recommendations

import (
	"math"
	"sort"
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
	Architecture    string `json:"architecture,omitempty"`
	ContextLength   int64  `json:"context_length,omitempty"`
	BlockCount      int64  `json:"block_count,omitempty"`
	Embedding       int64  `json:"embedding_length,omitempty"`
	HeadCount       int64  `json:"head_count,omitempty"`
	KVHeadCount     int64  `json:"kv_head_count,omitempty"`
	KeyLength       int64  `json:"key_length,omitempty"`
	ValueLength     int64  `json:"value_length,omitempty"`
	ExpertCount     int64  `json:"expert_count,omitempty"`
	ExpertUsedCount int64  `json:"expert_used_count,omitempty"`
}

type Capabilities struct {
	NCPUMoe bool `json:"n_cpu_moe"`
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
	NCPUMoe     int64    `json:"n_cpu_moe,omitempty"`
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
	PlacementRanges   PlacementRanges  `json:"placement_ranges"`
}

func Analyze(model models.Model, path string, snapshot hardware.Snapshot, requestedContext int64, hardwareErr error) Recommendation {
	return AnalyzeWithCapabilities(model, path, snapshot, requestedContext, hardwareErr, Capabilities{})
}

func AnalyzeWithCapabilities(model models.Model, path string, snapshot hardware.Snapshot, requestedContext int64, hardwareErr error, capabilities Capabilities) Recommendation {
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
	if metadata.ExpertCount > 0 && result.Confidence == "high" {
		result.Confidence = "medium"
	}
	result.CPUFit = fitsRAM(snapshot.RAMAvailableBytes, memory.CPUOnlyRAMBytes)
	classified := classifyOffloadWithCapabilities(snapshot, model.TotalBytes, contextLength, metadata, capabilities)
	result.CurrentFit, result.Offload = classified.Fit, classified.Offload
	result.TotalHardwareFit = totalHardwareFitWithCapabilities(snapshot, model.TotalBytes, contextLength, metadata, capabilities)
	result.PlacementRanges = ComputePlacementRangesWithCapabilities(snapshot, model.TotalBytes, metadata, capability, capabilities)
	return result
}

// AssumeIdleSnapshot returns a copy of snapshot with free VRAM/RAM treated as
// installed capacity. Only overwrites free/available when totals are known so
// test fixtures that set FreeBytes alone keep working.
func AssumeIdleSnapshot(snapshot hardware.Snapshot) hardware.Snapshot {
	return assumeIdleSnapshot(snapshot)
}

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
	demand := scheduler.EstimateDemand(scheduler.DemandInput{
		WeightsBytes: weights,
		Context:      context,
		Metadata: scheduler.KVMetadata{
			Architecture: metadata.Architecture, ContextLength: metadata.ContextLength, BlockCount: metadata.BlockCount,
			Embedding: metadata.Embedding, HeadCount: metadata.HeadCount, KVHeadCount: metadata.KVHeadCount,
			KeyLength: metadata.KeyLength, ValueLength: metadata.ValueLength, ExpertCount: metadata.ExpertCount,
		},
	})
	total := demand.WeightsBytes + demand.KVCacheBytes + demand.RuntimeOverheadBytes
	return MemoryEstimate{
		WeightsBytes: demand.WeightsBytes, KVCacheBytes: demand.KVCacheBytes, RuntimeOverheadBytes: demand.RuntimeOverheadBytes,
		CPUOnlyRAMBytes: total, FullOffloadVRAMBytes: total,
	}
}

func estimateKV(context int64, m Metadata) int64 {
	return scheduler.EstimateDemand(scheduler.DemandInput{Context: context, Metadata: scheduler.KVMetadata{
		Architecture: m.Architecture, ContextLength: m.ContextLength, BlockCount: m.BlockCount,
		Embedding: m.Embedding, HeadCount: m.HeadCount, KVHeadCount: m.KVHeadCount,
		KeyLength: m.KeyLength, ValueLength: m.ValueLength, ExpertCount: m.ExpertCount,
	}}).KVCacheBytes
}

func recommendOffload(snapshot hardware.Snapshot, memory MemoryEstimate, metadata Metadata) (bool, Offload) {
	return recommendOffloadWithCapabilities(snapshot, memory, metadata, Capabilities{})
}

func recommendOffloadWithCapabilities(snapshot hardware.Snapshot, memory MemoryEstimate, metadata Metadata, capabilities Capabilities) (bool, Offload) {
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

	moeFallbackPrefix := ""
	if metadata.ExpertCount > 0 {
		if capabilities.NCPUMoe {
			if fit, offload := recommendMoEOffload(snapshot, memory, metadata); fit {
				return true, offload
			}
		} else {
			moeFallbackPrefix = "MoE metadata was detected, but the active llama.cpp profile does not advertise --n-cpu-moe; using the standard dense fallback. "
		}
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
			return true, Offload{Mode: "cpu", Reason: moeFallbackPrefix + "GPU headroom is too small for useful offload, but the estimate fits in currently available system RAM."}
		}
		return false, Offload{Mode: "cpu", Reason: moeFallbackPrefix + "Current GPU headroom is too small for useful offload and available system RAM is below the conservative estimate."}
	}

	fixedGPU := memory.KVCacheBytes + memory.RuntimeOverheadBytes
	if best > fixedGPU {
		fraction := math.Min(1, float64(best-fixedGPU)/float64(memory.WeightsBytes))
		if fraction > 0 {
			offloadedWeights := int64(float64(memory.WeightsBytes) * fraction)
			cpuNeeded := memory.WeightsBytes - offloadedWeights
			if fitsRAM(snapshot.RAMAvailableBytes, cpuNeeded) {
				layers := recommendedLayers(metadata.BlockCount, fraction)
				return true, Offload{Mode: "partial", GPULayers: layers, Devices: []string{bestID}, KVOnGPU: true, Reason: moeFallbackPrefix + "Full offload does not fit currently; keep the KV cache on GPU and offload the largest useful share of model layers to the best single GPU."}
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
				return true, Offload{Mode: "hybrid", GPULayers: layers, Devices: []string{bestID}, KVOnGPU: false, Reason: moeFallbackPrefix + "The selected context makes an all-GPU KV cache too large. Keep KV in system RAM and use the available GPU for model-layer offload instead of falling back to CPU-only loading."}
			}
		}
	}

	if fitsRAM(snapshot.RAMAvailableBytes, memory.CPUOnlyRAMBytes) {
		return true, Offload{Mode: "cpu", Reason: moeFallbackPrefix + "The current GPU/RAM combination cannot satisfy a conservative offload plan, but CPU-only loading fits available system RAM."}
	}
	return false, Offload{Mode: "cpu", Reason: moeFallbackPrefix + "Current free GPU and system memory are below the conservative estimate for this context size."}
}

type usableGPU struct {
	id    string
	bytes int64
}

func recommendMoEOffload(snapshot hardware.Snapshot, memory MemoryEstimate, metadata Metadata) (bool, Offload) {
	if metadata.ExpertCount <= 0 || metadata.BlockCount <= 0 || memory.WeightsBytes <= 0 {
		return false, Offload{}
	}
	usable := make([]usableGPU, 0, len(snapshot.GPUs))
	var pooled int64
	for _, gpu := range snapshot.GPUs {
		available := gpu.FreeBytes - defaultVRAMReserve
		if available <= 0 {
			continue
		}
		usable = append(usable, usableGPU{id: gpu.ID, bytes: available})
		pooled += available
	}
	if len(usable) == 0 || pooled <= 0 {
		return false, Offload{}
	}
	sort.SliceStable(usable, func(i, j int) bool {
		if usable[i].bytes != usable[j].bytes {
			return usable[i].bytes > usable[j].bytes
		}
		return usable[i].id < usable[j].id
	})
	for {
		offload, pooledOK, devicesOK := planMoEOffload(snapshot, memory, metadata, usable)
		if pooledOK && devicesOK {
			return true, offload
		}
		if !pooledOK || len(usable) == 1 {
			return false, Offload{}
		}
		usable = usable[:len(usable)-1]
	}
}

func planMoEOffload(snapshot hardware.Snapshot, memory MemoryEstimate, metadata Metadata, usable []usableGPU) (Offload, bool, bool) {
	devices := make([]string, 0, len(usable))
	weights := make([]int64, 0, len(usable))
	var pooled int64
	for _, gpu := range usable {
		devices = append(devices, gpu.id)
		weights = append(weights, gpu.bytes)
		pooled += gpu.bytes
	}
	tensorSplit := moeTensorSplit(weights)
	demand := func(blocks int64, kvOnGPU bool) (gpuNeeded, hostNeeded int64) {
		gpuWeights, hostWeights := scheduler.MoEWeightDistribution(memory.WeightsBytes, metadata.BlockCount, blocks, metadata.ExpertCount)
		gpuNeeded = gpuWeights + memory.RuntimeOverheadBytes
		hostNeeded = hostWeights
		if kvOnGPU {
			gpuNeeded += memory.KVCacheBytes
		} else {
			hostNeeded += memory.KVCacheBytes
		}
		return gpuNeeded, hostNeeded
	}
	gpuFits := func(blocks int64, kvOnGPU bool) bool {
		gpuNeeded, _ := demand(blocks, kvOnGPU)
		return gpuNeeded <= pooled && splitFitsDevices(gpuNeeded, weights)
	}
	pooledGPUFits := func(blocks int64, kvOnGPU bool) bool {
		gpuNeeded, _ := demand(blocks, kvOnGPU)
		return gpuNeeded <= pooled
	}
	ramOK := func(blocks int64, kvOnGPU bool) bool {
		_, hostNeeded := demand(blocks, kvOnGPU)
		return fitsRAM(snapshot.RAMAvailableBytes, hostNeeded)
	}

	// GPU demand falls as n-cpu-moe rises; host RAM rises. Search the minimum
	// spill from GPU constraints first, then validate RAM at that point.
	if gpuFits(metadata.BlockCount, true) {
		lo, hi := int64(0), metadata.BlockCount
		for lo < hi {
			mid := lo + (hi-lo)/2
			if gpuFits(mid, true) {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		if ramOK(lo, true) {
			return Offload{
				Mode: "moe", GPULayers: metadata.BlockCount, NCPUMoe: lo,
				Devices: devices, TensorSplit: tensorSplit, KVOnGPU: true,
				Reason: "Full GPU offload does not fit. Keep all transformer layers and the KV cache on the currently free GPUs while placing only the routed expert weights required to fit in system RAM. Expert weight size is conservatively estimated from GGUF size.",
			}, true, true
		}
		// Larger n only increases host RAM, and dropping GPUs would raise n.
		return Offload{}, false, false
	}

	// KV-to-RAM cliff: only when even full expert spill cannot keep KV in VRAM.
	if !pooledGPUFits(metadata.BlockCount, true) {
		if gpuFits(metadata.BlockCount, false) && ramOK(metadata.BlockCount, false) {
			return Offload{
				Mode: "moe", GPULayers: metadata.BlockCount, NCPUMoe: metadata.BlockCount,
				Devices: devices, TensorSplit: tensorSplit, KVOnGPU: false,
				Reason: "Even with all routed experts in system RAM, the KV cache does not fit within current VRAM headroom. Keep the same GPU set and full expert spill, then move the KV cache to system RAM. Expert weight size is conservatively estimated from GGUF size.",
			}, true, true
		}
		if pooledGPUFits(metadata.BlockCount, false) && !ramOK(metadata.BlockCount, false) {
			return Offload{}, false, false
		}
	}

	pooledOK := pooledGPUFits(metadata.BlockCount, true) || pooledGPUFits(metadata.BlockCount, false)
	return Offload{}, pooledOK, false
}

func moeSplitWeights(bytes []int64) []int64 {
	const unit = int64(256 * mib)
	out := make([]int64, len(bytes))
	for i, value := range bytes {
		weight := value / unit
		if weight < 1 {
			weight = 1
		}
		out[i] = weight
	}
	return out
}

func moeTensorSplit(bytes []int64) string {
	if len(bytes) < 2 {
		return ""
	}
	parts := make([]string, 0, len(bytes))
	for _, weight := range moeSplitWeights(bytes) {
		parts = append(parts, itoa(weight))
	}
	return strings.Join(parts, ",")
}

func splitFitsDevices(gpuNeeded int64, available []int64) bool {
	if len(available) == 0 {
		return false
	}
	if len(available) == 1 {
		return gpuNeeded <= available[0]
	}
	weights := moeSplitWeights(available)
	var sum int64
	for _, weight := range weights {
		sum += weight
	}
	assigned := int64(0)
	for i, avail := range available {
		var share int64
		if i == len(available)-1 {
			share = gpuNeeded - assigned
		} else {
			share = gpuNeeded * weights[i] / sum
			assigned += share
		}
		if share > avail {
			return false
		}
	}
	return true
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
	metadata := Metadata{
		Architecture: d.Architecture, ContextLength: d.ContextLength, BlockCount: d.BlockCount,
		Embedding: d.Embedding, HeadCount: d.HeadCount, KVHeadCount: d.KVHeadCount,
		KeyLength: d.KeyLength, ValueLength: d.ValueLength,
	}
	for _, entry := range inspection.Metadata {
		if entry.Key == d.Architecture+".expert_count" {
			metadata.ExpertCount = parseMetadataInt(entry.Value)
		}
		if entry.Key == d.Architecture+".expert_used_count" {
			metadata.ExpertUsedCount = parseMetadataInt(entry.Value)
		}
	}
	return metadata, nil
}

func parseMetadataInt(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	var result int64
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		if result > (math.MaxInt64-int64(r-'0'))/10 {
			return 0
		}
		result = result*10 + int64(r-'0')
	}
	return result
}
