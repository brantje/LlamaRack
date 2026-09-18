package recommendations

import (
	"os"
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

type RuntimeConfig struct {
	Options               map[string]string
	CompanionBytes        int64
	GPUMode               string
	GPUDevices            []string
	TensorSplit           string
	AllowSystemSpillover  bool
}

func AnalyzeRuntime(model models.Model, path string, snapshot hardware.Snapshot, requestedContext int64, hardwareErr error, capabilities Capabilities, runtime RuntimeConfig) Recommendation {
	metadata, metadataErr := ReadMetadata(path)
	contextLength, assumed := chooseContext(requestedContext)
	capability := int64(model.ContextLength)
	if capability <= 0 {
		capability = metadata.ContextLength
	}
	memory := runtimeMemory(model.TotalBytes, runtime.CompanionBytes, contextLength, metadata, runtime.Options)
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

	if hardwareErr != nil {
		result.Offload = Offload{Mode: "cpu", Reason: "Hardware availability is unknown, so runnable placement cannot be confirmed."}
		result.PlacementRanges = PlacementRanges{
			MinimumContext: placementMinContext, ContextStep: placementContextStep,
			UnavailableReason: "Hardware availability is unknown.",
		}
		return result
	}

	idle := assumeIdleSnapshot(snapshot)
	request := runtimeRequest(snapshot, &idle, model.TotalBytes, runtime.CompanionBytes, contextLength, metadata, metadataErr, capabilities, runtime)
	plan, err := scheduler.PlanRuntime(request)
	if err == nil {
		result.CurrentFit = plan.Fits
		result.Offload = offloadFromRuntimePlan(plan, metadata)
	}
	idleRequest := request
	idleRequest.Snapshot = idle
	idleRequest.IdleSnapshot = nil
	if plan, err := scheduler.PlanRuntime(idleRequest); err == nil {
		result.TotalHardwareFit = plan.Fits
	}
	cpuRequest := request
	cpuRequest.Snapshot = hardware.Snapshot{RAMTotalBytes: snapshot.RAMTotalBytes, RAMAvailableBytes: snapshot.RAMAvailableBytes}
	cpuRequest.IdleSnapshot = nil
	cpuRequest.AllowSystemSpillover = true
	if plan, err := scheduler.PlanRuntime(cpuRequest); err == nil {
		result.CPUFit = plan.Fits
	}
	result.PlacementRanges = ComputeRuntimePlacementRanges(snapshot, model.TotalBytes, runtime.CompanionBytes, metadata, metadataErr, capability, capabilities, runtime)
	return result
}

func ComputeRuntimePlacementRanges(snapshot hardware.Snapshot, weights, companionBytes int64, metadata Metadata, metadataErr error, capability int64, capabilities Capabilities, runtime RuntimeConfig) PlacementRanges {
	ranges := PlacementRanges{MinimumContext: placementMinContext, ContextStep: placementContextStep}
	if capability <= 0 {
		ranges.UnavailableReason = "Model context capability is unknown."
		return ranges
	}
	if estimateKV(placementMinContext, metadata) <= 0 {
		ranges.UnavailableReason = "LlamaRack could not determine reliable context boundaries for this Model."
		return ranges
	}
	maxCapability := alignContextDown(capability, placementContextStep)
	if maxCapability < placementMinContext {
		ranges.UnavailableReason = "Model context capability is below the selectable context step."
		return ranges
	}

	idle := assumeIdleSnapshot(snapshot)
	var current *classifiedPlacement
	start := int64(0)
	lastFit := int64(0)
	for context := placementMinContext; context <= maxCapability; context += placementContextStep {
		classified := classifyRuntime(snapshot, &idle, weights, companionBytes, context, metadata, metadataErr, capabilities, runtime)
		if !classified.Fit {
			if current != nil {
				zone := placementZoneFrom(start, context-placementContextStep, *current)
				zone.TotalHardwareFit = runtimeTotalHardwareFit(idle, weights, companionBytes, start, metadata, metadataErr, capabilities, runtime)
				ranges.Zones = append(ranges.Zones, zone)
				current = nil
			}
			continue
		}
		lastFit = context
		if classified.Offload.Mode == "full" || classified.Offload.Mode == "multi_gpu" {
			ranges.GPUOnlyMaxContext = context
		}
		if current == nil {
			copy := classified
			current = &copy
			start = context
			continue
		}
		if placementIdentity(classified) != placementIdentity(*current) {
			zone := placementZoneFrom(start, context-placementContextStep, *current)
			zone.TotalHardwareFit = runtimeTotalHardwareFit(idle, weights, companionBytes, start, metadata, metadataErr, capabilities, runtime)
			ranges.Zones = append(ranges.Zones, zone)
			copy := classified
			current = &copy
			start = context
		}
	}
	if current != nil {
		zone := placementZoneFrom(start, lastFit, *current)
		zone.TotalHardwareFit = runtimeTotalHardwareFit(idle, weights, companionBytes, start, metadata, metadataErr, capabilities, runtime)
		ranges.Zones = append(ranges.Zones, zone)
	}
	if lastFit == 0 {
		ranges.UnavailableReason = "No selectable context can be admitted with the current runtime policy and available resources."
		return ranges
	}
	ranges.Available = true
	ranges.MaximumContext = lastFit
	return ranges
}

func classifyRuntime(snapshot hardware.Snapshot, idle *hardware.Snapshot, weights, companionBytes, context int64, metadata Metadata, metadataErr error, capabilities Capabilities, runtime RuntimeConfig) classifiedPlacement {
	request := runtimeRequest(snapshot, idle, weights, companionBytes, context, metadata, metadataErr, capabilities, runtime)
	plan, err := scheduler.PlanRuntime(request)
	if err != nil {
		return classifiedPlacement{}
	}
	return classifiedPlacement{Fit: plan.Fits, Offload: offloadFromRuntimePlan(plan, metadata)}
}

func runtimeTotalHardwareFit(idle hardware.Snapshot, weights, companionBytes, context int64, metadata Metadata, metadataErr error, capabilities Capabilities, runtime RuntimeConfig) bool {
	request := runtimeRequest(idle, nil, weights, companionBytes, context, metadata, metadataErr, capabilities, runtime)
	plan, err := scheduler.PlanRuntime(request)
	return err == nil && plan.Fits
}

func runtimeRequest(snapshot hardware.Snapshot, idle *hardware.Snapshot, weights, companionBytes, context int64, metadata Metadata, metadataErr error, capabilities Capabilities, runtime RuntimeConfig) scheduler.RuntimePlanRequest {
	options := cloneRuntimeOptions(runtime.Options)
	return scheduler.RuntimePlanRequest{
		Snapshot: snapshot,
		IdleSnapshot: idle,
		Demand: scheduler.DemandInput{
			WeightsBytes: weights, CompanionBytes: companionBytes, Context: context,
			Metadata: scheduler.KVMetadata{
				Architecture: metadata.Architecture, ContextLength: metadata.ContextLength, BlockCount: metadata.BlockCount,
				Embedding: metadata.Embedding, HeadCount: metadata.HeadCount, KVHeadCount: metadata.KVHeadCount,
				KeyLength: metadata.KeyLength, ValueLength: metadata.ValueLength, ExpertCount: metadata.ExpertCount,
			},
			MetadataErr: metadataErr,
			Options: options,
		},
		Placement: scheduler.PlacementRequest{
			Mode: runtime.GPUMode, Devices: append([]string(nil), runtime.GPUDevices...), TensorSplit: runtime.TensorSplit,
		},
		AllowSystemSpillover: runtime.AllowSystemSpillover,
		Capabilities: scheduler.RuntimeCapabilities{
			NCPUMoe: capabilities.NCPUMoe, CPUMoe: capabilities.CPUMoe,
			NoKVOffload: capabilities.NoKVOffload, GPULayers: capabilities.GPULayers,
		},
	}
}

func runtimeMemory(weights, companionBytes, context int64, metadata Metadata, options map[string]string) MemoryEstimate {
	full := cloneRuntimeOptions(options)
	for _, key := range []string{"gpu-layers", "n-gpu-layers", "cpu-moe", "n-cpu-moe", "no-kv-offload"} {
		delete(full, key)
		delete(full, "--"+key)
	}
	demand := scheduler.EstimateDemand(scheduler.DemandInput{
		WeightsBytes: weights, CompanionBytes: companionBytes, Context: context,
		Metadata: scheduler.KVMetadata{
			Architecture: metadata.Architecture, ContextLength: metadata.ContextLength, BlockCount: metadata.BlockCount,
			Embedding: metadata.Embedding, HeadCount: metadata.HeadCount, KVHeadCount: metadata.KVHeadCount,
			KeyLength: metadata.KeyLength, ValueLength: metadata.ValueLength, ExpertCount: metadata.ExpertCount,
		},
		Options: full,
	})
	cpuOptions := cloneRuntimeOptions(full)
	cpuOptions["n-gpu-layers"] = "0"
	cpu := scheduler.EstimateDemand(scheduler.DemandInput{
		WeightsBytes: weights, CompanionBytes: companionBytes, Context: context,
		Metadata: scheduler.KVMetadata{
			Architecture: metadata.Architecture, ContextLength: metadata.ContextLength, BlockCount: metadata.BlockCount,
			Embedding: metadata.Embedding, HeadCount: metadata.HeadCount, KVHeadCount: metadata.KVHeadCount,
			KeyLength: metadata.KeyLength, ValueLength: metadata.ValueLength, ExpertCount: metadata.ExpertCount,
		},
		Options: cpuOptions,
	})
	return MemoryEstimate{
		WeightsBytes: demand.WeightsBytes, KVCacheBytes: demand.KVCacheBytes, RuntimeOverheadBytes: demand.RuntimeOverheadBytes,
		CPUOnlyRAMBytes: cpu.HostRAMBytes, FullOffloadVRAMBytes: demand.VRAMBytes(),
	}
}

func offloadFromRuntimePlan(plan scheduler.RuntimePlan, metadata Metadata) Offload {
	layers := metadata.BlockCount
	if raw := runtimeOption(plan.Options, "n-gpu-layers"); raw != "" {
		if parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
			if parsed < 0 {
				layers = metadata.BlockCount
			} else {
				layers = parsed
			}
		}
	}
	nCPUMoe := int64(0)
	if runtimeOptionEnabled(plan.Options, "cpu-moe") {
		nCPUMoe = metadata.BlockCount
	} else if raw := runtimeOption(plan.Options, "n-cpu-moe"); raw != "" {
		nCPUMoe, _ = strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	}
	devices := append([]string(nil), plan.Placement.Devices...)
	tensorSplit := plan.Placement.TensorSplit
	kvOnGPU := !runtimeOptionEnabled(plan.Options, "no-kv-offload")
	if plan.Mode == "cpu" {
		layers = 0
		devices = nil
		tensorSplit = ""
		kvOnGPU = false
	}
	return Offload{
		Mode: plan.Mode, GPULayers: layers, NCPUMoe: nCPUMoe,
		Devices: devices, TensorSplit: tensorSplit,
		KVOnGPU: kvOnGPU,
		Reason: runtimePlanReason(plan),
	}
}

func runtimePlanReason(plan scheduler.RuntimePlan) string {
	if !plan.Fits {
		return "The selected runtime policy cannot be admitted with the currently available GPU and host RAM."
	}
	switch plan.Mode {
	case "full":
		return "The exact launch demand fits on one GPU with scheduler headroom."
	case "multi_gpu":
		return "The exact launch demand fits across the selected GPU set with scheduler headroom."
	case "moe":
		return "The exact launch demand fits by placing routed expert weights in host RAM."
	case "partial":
		return "The exact launch demand fits by keeping KV on GPU and spilling part of the model weights to host RAM."
	case "hybrid":
		return "The exact launch demand fits by moving KV and part of the model weights to host RAM."
	case "cpu":
		return "The exact launch demand fits in host RAM without a GPU reservation."
	default:
		return "The exact launch demand fits the selected runtime policy."
	}
}

func CompanionBytes(options map[string]string) int64 {
	total := int64(0)
	for _, key := range []string{"mmproj", "spec-draft-model"} {
		path := strings.TrimSpace(runtimeOption(options, key))
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		total += info.Size()
	}
	return total
}

func cloneRuntimeOptions(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func runtimeOption(options map[string]string, key string) string {
	if value, ok := options[key]; ok {
		return value
	}
	return options["--"+key]
}

func runtimeOptionEnabled(options map[string]string, key string) bool {
	switch strings.ToLower(strings.TrimSpace(runtimeOption(options, key))) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}
