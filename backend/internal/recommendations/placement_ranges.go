package recommendations

import (
	"strings"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

const (
	placementContextStep = int64(512)
	placementMinContext  = int64(512)
)

type PlacementZone struct {
	StartContext     int64    `json:"start_context"`
	EndContext       int64    `json:"end_context"`
	Kind             string   `json:"kind"`
	OffloadMode      string   `json:"offload_mode"`
	GPUCount         int      `json:"gpu_count"`
	Devices          []string `json:"devices,omitempty"`
	KVOnGPU          bool     `json:"kv_on_gpu"`
	GPULayers        int64    `json:"gpu_layers,omitempty"`
	TensorSplit      string   `json:"tensor_split,omitempty"`
	CurrentFit       bool     `json:"current_fit"`
	TotalHardwareFit bool     `json:"total_hardware_fit"`
}

type PlacementRanges struct {
	Available         bool            `json:"available"`
	UnavailableReason string          `json:"unavailable_reason,omitempty"`
	MinimumContext    int64           `json:"minimum_context"`
	MaximumContext    int64           `json:"maximum_context"`
	ContextStep       int64           `json:"context_step"`
	GPUOnlyMaxContext int64           `json:"gpu_only_max_context,omitempty"`
	Zones             []PlacementZone `json:"zones,omitempty"`
}

type classifiedPlacement struct {
	Fit     bool
	Offload Offload
}

func ComputePlacementRanges(snapshot hardware.Snapshot, weights int64, metadata Metadata, capability int64) PlacementRanges {
	ranges := PlacementRanges{
		MinimumContext: placementMinContext,
		ContextStep:    placementContextStep,
	}
	if capability <= 0 {
		ranges.UnavailableReason = "Model context capability is unknown."
		return ranges
	}
	if estimateKV(placementMinContext, metadata) <= 0 {
		ranges.UnavailableReason = "LlamaRack could not determine reliable context boundaries for this Model."
		return ranges
	}
	if capability < placementMinContext {
		ranges.UnavailableReason = "Model context capability is below the selectable context step."
		return ranges
	}
	maximum := alignContextDown(capability, placementContextStep)
	if maximum < placementMinContext {
		ranges.UnavailableReason = "Model context capability is below the selectable context step."
		return ranges
	}
	ranges.Available = true
	ranges.MaximumContext = maximum

	start := placementMinContext
	for start <= maximum {
		current := classifyOffload(snapshot, weights, start, metadata)
		identity := placementIdentity(current)
		end := lastMatchingContext(snapshot, weights, metadata, start, maximum, identity)
		zone := placementZoneFrom(start, end, current)
		zone.TotalHardwareFit = totalHardwareFit(snapshot, weights, start, metadata)
		ranges.Zones = append(ranges.Zones, zone)
		if zone.Kind == "gpu" {
			ranges.GPUOnlyMaxContext = zone.EndContext
		}
		next := end + placementContextStep
		if next <= start {
			break
		}
		start = next
	}
	return ranges
}

func classifyOffload(snapshot hardware.Snapshot, weights, context int64, metadata Metadata) classifiedPlacement {
	memory := estimateMemory(weights, context, metadata)
	if len(snapshot.GPUs) == 0 {
		if fitsRAM(snapshot.RAMAvailableBytes, memory.CPUOnlyRAMBytes) {
			return classifiedPlacement{Fit: true, Offload: Offload{Mode: "cpu", Reason: "No GPU was detected; the estimated model and KV cache fit in currently available system RAM."}}
		}
		return classifiedPlacement{Fit: false, Offload: Offload{Mode: "cpu", Reason: "No GPU was detected and currently available system RAM is below the conservative estimate."}}
	}
	fit, offload := recommendOffload(snapshot, memory, metadata)
	return classifiedPlacement{Fit: fit, Offload: offload}
}

func totalHardwareFit(snapshot hardware.Snapshot, weights, context int64, metadata Metadata) bool {
	memory := estimateMemory(weights, context, metadata)
	idle := assumeIdleSnapshot(snapshot)
	if len(idle.GPUs) == 0 {
		return fitsRAM(snapshot.RAMTotalBytes, memory.CPUOnlyRAMBytes)
	}
	return classifyOffload(idle, weights, context, metadata).Fit
}

func lastMatchingContext(snapshot hardware.Snapshot, weights int64, metadata Metadata, start, maximum int64, identity string) int64 {
	low, high := start, maximum
	last := start
	for low <= high {
		mid := alignContext((low+high)/2, placementContextStep)
		if mid < low {
			mid = low
		}
		if mid > high {
			mid = alignContext(high, placementContextStep)
			if mid < low {
				break
			}
		}
		if placementIdentity(classifyOffload(snapshot, weights, mid, metadata)) == identity {
			last = mid
			low = mid + placementContextStep
			continue
		}
		high = mid - placementContextStep
	}
	return last
}

func placementZoneFrom(start, end int64, current classifiedPlacement) PlacementZone {
	devices := append([]string(nil), current.Offload.Devices...)
	kind, gpuCount := placementKind(current)
	return PlacementZone{
		StartContext: start,
		EndContext:   end,
		Kind:         kind,
		OffloadMode:  current.Offload.Mode,
		GPUCount:     gpuCount,
		Devices:      devices,
		KVOnGPU:      current.Offload.KVOnGPU,
		GPULayers:    current.Offload.GPULayers,
		TensorSplit:  current.Offload.TensorSplit,
		CurrentFit:   current.Fit,
	}
}

func placementKind(current classifiedPlacement) (string, int) {
	switch current.Offload.Mode {
	case "full":
		return "gpu", 1
	case "multi_gpu":
		count := len(current.Offload.Devices)
		if count < 2 {
			count = 2
		}
		return "gpu", count
	case "partial":
		return "partial", len(current.Offload.Devices)
	case "hybrid":
		return "hybrid", len(current.Offload.Devices)
	default:
		if current.Fit {
			return "cpu", 0
		}
		return "no_fit", 0
	}
}

func placementIdentity(current classifiedPlacement) string {
	kind, gpuCount := placementKind(current)
	parts := []string{kind, itoa(int64(gpuCount)), strings.Join(current.Offload.Devices, ","), offloadBool(current.Offload.KVOnGPU), offloadBool(current.Fit)}
	return strings.Join(parts, "|")
}

func alignContext(value, step int64) int64 {
	if step <= 0 {
		return value
	}
	if value < step {
		return step
	}
	return alignContextDown(value, step)
}

func alignContextDown(value, step int64) int64 {
	if step <= 0 {
		return value
	}
	return value - (value % step)
}

func offloadBool(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var buffer [24]byte
	pos := len(buffer)
	for value > 0 {
		pos--
		buffer[pos] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		pos--
		buffer[pos] = '-'
	}
	return string(buffer[pos:])
}
