package scheduler

import (
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

const defaultRAMReserveBytes int64 = 1024 * 1024 * 1024

type RuntimeCapabilities struct {
	NCPUMoe          bool
	CPUMoe           bool
	NoKVOffload      bool
	GPULayers        bool
	GPULayersOption  string
}

type RuntimePlanRequest struct {
	Snapshot             hardware.Snapshot
	IdleSnapshot         *hardware.Snapshot
	Demand               DemandInput
	Placement            PlacementRequest
	AllowSystemSpillover bool
	Capabilities         RuntimeCapabilities
}

type RuntimePlan struct {
	Fits              bool
	Mode              string
	Demand            ResourceDemand
	Placement         Placement
	Options           map[string]string
	RequiresSpillover bool
}

func PlanRuntime(req RuntimePlanRequest) (RuntimePlan, error) {
	base := cloneStringMap(req.Demand.Options)
	locks := runtimeOptionLocksFor(base)
	if req.IdleSnapshot != nil && !req.AllowSystemSpillover && req.Capabilities.NCPUMoe && !locks.MoE {
		idleReq := req
		idleReq.Snapshot = *req.IdleSnapshot
		idleReq.IdleSnapshot = nil
		idlePlan, err := PlanRuntime(idleReq)
		if err == nil && idlePlan.Fits && (idlePlan.Mode == "full" || idlePlan.Mode == "multi_gpu") {
			req.Capabilities.NCPUMoe = false
			req.Capabilities.CPUMoe = false
		}
	}
	if len(req.Snapshot.GPUs) == 0 {
		if locks.GPULayers {
			return evaluateRuntimeCandidate(req, base, false)
		}
		cpuDemandOptions := cloneStringMap(base)
		cpuDemandOptions["n-gpu-layers"] = "0"
		options := cloneStringMap(base)
		if key := gpuLayerOptionKey(req.Capabilities); key != "" {
			options[key] = "0"
			cpuDemandOptions = options
		}
		demand := demandFor(req.Demand, cpuDemandOptions)
		return RuntimePlan{
			Fits: runtimeHostRAMFits(req.Snapshot, demand.HostRAMBytes),
			Mode: "cpu", Demand: demand, Placement: Placement{RequiredBytes: 0, Fits: true},
			Options: options,
		}, nil
	}

	full, err := evaluateRuntimeCandidate(req, base, false)
	if err != nil || full.Fits {
		return full, err
	}

	if req.Demand.Metadata.ExpertCount > 0 && req.Capabilities.NCPUMoe && !locks.MoE {
		if moe, ok, err := planAutomaticMoE(req, base, locks); err != nil {
			return RuntimePlan{}, err
		} else if ok {
			return moe, nil
		}
	}

	if !req.AllowSystemSpillover {
		return full, nil
	}

	if !locks.GPULayers && gpuLayerOptionKey(req.Capabilities) != "" {
		if partial, ok, err := planDensePartial(req, base, false); err != nil {
			return RuntimePlan{}, err
		} else if ok {
			return partial, nil
		}
	}

	if !locks.KV && req.Capabilities.NoKVOffload {
		if !locks.GPULayers && gpuLayerOptionKey(req.Capabilities) != "" {
			if hybrid, ok, err := planDensePartial(req, base, true); err != nil {
				return RuntimePlan{}, err
			} else if ok {
				return hybrid, nil
			}
		} else {
			options := cloneStringMap(base)
			options["no-kv-offload"] = "true"
			kvPlan, err := evaluateRuntimeCandidate(req, options, true)
			if err != nil {
				return RuntimePlan{}, err
			}
			if kvPlan.Fits {
				kvPlan.RequiresSpillover = true
				return kvPlan, nil
			}
		}
	}

	if !locks.GPULayers {
		if key := gpuLayerOptionKey(req.Capabilities); key != "" {
			cpuOptions := cloneStringMap(base)
			cpuOptions[key] = "0"
			cpu, err := evaluateRuntimeCandidate(req, cpuOptions, true)
			if err != nil {
				return RuntimePlan{}, err
			}
			if cpu.Fits {
				cpu.Mode = "cpu"
				cpu.RequiresSpillover = true
				return cpu, nil
			}
		}
	}
	return full, nil
}

type runtimeOptionLocks struct {
	GPULayers bool
	MoE       bool
	KV        bool
}

func runtimeOptionLocksFor(options map[string]string) runtimeOptionLocks {
	return runtimeOptionLocks{
		GPULayers: optionPresent(options, "gpu-layers") || optionPresent(options, "n-gpu-layers"),
		MoE:       optionPresent(options, "cpu-moe") || optionPresent(options, "n-cpu-moe"),
		KV:        optionPresent(options, "no-kv-offload") || optionPresent(options, "kv-offload"),
	}
}

func optionPresent(options map[string]string, key string) bool {
	if options == nil {
		return false
	}
	if _, ok := options[key]; ok {
		return true
	}
	_, ok := options["--"+key]
	return ok
}

func gpuLayerOptionKey(capabilities RuntimeCapabilities) string {
	if key := strings.TrimSpace(capabilities.GPULayersOption); key != "" {
		return strings.TrimLeft(key, "-")
	}
	if capabilities.GPULayers {
		return "n-gpu-layers"
	}
	return ""
}

func gpuLayerOptionValue(options map[string]string) string {
	if value := optionValue(options, "gpu-layers"); strings.TrimSpace(value) != "" {
		return value
	}
	return optionValue(options, "n-gpu-layers")
}

func planAutomaticMoE(req RuntimePlanRequest, base map[string]string, locks runtimeOptionLocks) (RuntimePlan, bool, error) {
	blocks := req.Demand.Metadata.BlockCount
	if blocks <= 0 {
		return RuntimePlan{}, false, nil
	}
	options := cloneStringMap(base)
	lo, hi := int64(1), blocks
	firstGPUFit := int64(0)
	for lo <= hi {
		mid := lo + (hi-lo)/2
		probe := cloneStringMap(options)
		probe["n-cpu-moe"] = strconv.FormatInt(mid, 10)
		demand := demandFor(req.Demand, probe)
		placement, err := planDemandPlacement(req.Placement, req.Snapshot, demand)
		if err != nil {
			return RuntimePlan{}, false, err
		}
		if placement.Fits && runtimeHostRAMFits(req.Snapshot, demand.HostRAMBytes) {
			firstGPUFit = mid
			hi = mid - 1
		} else {
			lo = mid + 1
		}
	}
	if firstGPUFit > 0 {
		options["n-cpu-moe"] = strconv.FormatInt(firstGPUFit, 10)
		if firstGPUFit >= blocks && req.Capabilities.CPUMoe {
			delete(options, "n-cpu-moe")
			options["cpu-moe"] = "true"
		}
		plan, err := evaluateRuntimeCandidate(req, options, false)
		if err != nil {
			return RuntimePlan{}, false, err
		}
		if plan.Fits {
			plan.Mode = "moe"
			return plan, true, nil
		}
	}

	options = cloneStringMap(base)
	if req.Capabilities.CPUMoe {
		options["cpu-moe"] = "true"
	} else {
		options["n-cpu-moe"] = strconv.FormatInt(blocks, 10)
	}
	if !optionEnabled(options, "no-kv-offload") {
		if locks.KV || !req.Capabilities.NoKVOffload {
			return RuntimePlan{}, false, nil
		}
		options["no-kv-offload"] = "true"
	}
	plan, err := evaluateRuntimeCandidate(req, options, false)
	if err != nil {
		return RuntimePlan{}, false, err
	}
	if plan.Fits {
		plan.Mode = "moe"
		return plan, true, nil
	}
	return RuntimePlan{}, false, nil
}

func planDensePartial(req RuntimePlanRequest, base map[string]string, moveKV bool) (RuntimePlan, bool, error) {
	blocks := req.Demand.Metadata.BlockCount
	key := gpuLayerOptionKey(req.Capabilities)
	if blocks <= 0 || key == "" {
		return RuntimePlan{}, false, nil
	}
	options := cloneStringMap(base)
	if moveKV {
		options["no-kv-offload"] = "true"
	}
	lo, hi := int64(1), blocks
	best := int64(0)
	for lo <= hi {
		mid := lo + (hi-lo)/2
		probe := cloneStringMap(options)
		probe[key] = strconv.FormatInt(mid, 10)
		demand := demandFor(req.Demand, probe)
		placement, err := planDemandPlacement(req.Placement, req.Snapshot, demand)
		if err != nil {
			return RuntimePlan{}, false, err
		}
		if placement.Fits && runtimeHostRAMFits(req.Snapshot, demand.HostRAMBytes) {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best <= 0 {
		return RuntimePlan{}, false, nil
	}
	options[key] = strconv.FormatInt(best, 10)
	plan, err := evaluateRuntimeCandidate(req, options, true)
	if err != nil {
		return RuntimePlan{}, false, err
	}
	if !plan.Fits {
		return RuntimePlan{}, false, nil
	}
	plan.RequiresSpillover = true
	return plan, true, nil
}

func evaluateRuntimeCandidate(req RuntimePlanRequest, options map[string]string, spill bool) (RuntimePlan, error) {
	demand := demandFor(req.Demand, options)
	placement, err := planDemandPlacement(req.Placement, req.Snapshot, demand)
	if err != nil {
		return RuntimePlan{}, err
	}
	fits := placement.Fits && runtimeHostRAMFits(req.Snapshot, demand.HostRAMBytes)
	mode := candidateMode(options, demand, placement)
	return RuntimePlan{
		Fits: fits, Mode: mode, Demand: demand, Placement: placement,
		Options: cloneStringMap(options), RequiresSpillover: spill,
	}, nil
}

func demandFor(base DemandInput, options map[string]string) ResourceDemand {
	base.Options = options
	return EstimateDemand(base)
}

func planDemandPlacement(base PlacementRequest, snapshot hardware.Snapshot, demand ResourceDemand) (Placement, error) {
	required := demand.VRAMBytes()
	if required == 0 {
		return Placement{RequiredBytes: 0, Fits: true}, nil
	}
	request := base
	request.RequiredBytes = required
	request.HostRAMBytes = demand.HostRAMBytes
	request.SplittableBytes = demand.GPUSplittableBytes
	request.FixedBytes = demand.GPUFixedBytes
	return PlanPlacement(snapshot, request)
}

func runtimeHostRAMFits(snapshot hardware.Snapshot, required int64) bool {
	if required <= 0 {
		return true
	}
	if snapshot.RAMTotalBytes <= 0 && snapshot.RAMAvailableBytes <= 0 {
		return false
	}
	if snapshot.RAMAvailableBytes <= defaultRAMReserveBytes {
		return false
	}
	return snapshot.RAMAvailableBytes-defaultRAMReserveBytes >= required
}

func candidateMode(options map[string]string, demand ResourceDemand, placement Placement) string {
	if demand.VRAMBytes() == 0 {
		return "cpu"
	}
	if optionEnabled(options, "cpu-moe") || strings.TrimSpace(optionValue(options, "n-cpu-moe")) != "" {
		return "moe"
	}
	if optionEnabled(options, "no-kv-offload") {
		return "hybrid"
	}
	if raw := strings.TrimSpace(gpuLayerOptionValue(options)); raw != "" {
		if layers, err := strconv.ParseInt(raw, 10, 64); err == nil && layers > 0 {
			return "partial"
		}
	}
	if len(placement.Devices) > 1 {
		return "multi_gpu"
	}
	return "full"
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+4)
	for key, value := range in {
		out[key] = value
	}
	return out
}
