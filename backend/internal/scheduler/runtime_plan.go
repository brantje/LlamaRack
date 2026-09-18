package scheduler

import (
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

const defaultRAMReserveBytes int64 = 1024 * 1024 * 1024

type RuntimeCapabilities struct {
	NCPUMoe     bool
	CPUMoe      bool
	NoKVOffload bool
	GPULayers   bool
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
	if req.IdleSnapshot != nil && !req.AllowSystemSpillover && req.Capabilities.NCPUMoe && !hasExplicitOffload(base) {
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
		cpuDemandOptions := cloneStringMap(base)
		cpuDemandOptions["n-gpu-layers"] = "0"
		demand := demandFor(req.Demand, cpuDemandOptions)
		options := cloneStringMap(base)
		if req.Capabilities.GPULayers {
			options["n-gpu-layers"] = "0"
		}
		return RuntimePlan{
			Fits: runtimeHostRAMFits(req.Snapshot, demand.HostRAMBytes),
			Mode: "cpu", Demand: demand, Placement: Placement{RequiredBytes: 0, Fits: true},
			Options: options,
		}, nil
	}
	if hasExplicitOffload(base) {
		return evaluateRuntimeCandidate(req, base, false)
	}
	full, err := evaluateRuntimeCandidate(req, base, false)
	if err != nil || full.Fits {
		return full, err
	}
	if req.Demand.Metadata.ExpertCount > 0 && req.Capabilities.NCPUMoe {
		if moe, ok, err := planAutomaticMoE(req, base); err != nil {
			return RuntimePlan{}, err
		} else if ok {
			return moe, nil
		}
	}
	if !req.AllowSystemSpillover || !req.Capabilities.GPULayers {
		return full, nil
	}
	if partial, ok, err := planDensePartial(req, base, false); err != nil {
		return RuntimePlan{}, err
	} else if ok {
		return partial, nil
	}
	if req.Capabilities.NoKVOffload {
		if hybrid, ok, err := planDensePartial(req, base, true); err != nil {
			return RuntimePlan{}, err
		} else if ok {
			return hybrid, nil
		}
	}
	cpuOptions := cloneStringMap(base)
	cpuOptions["n-gpu-layers"] = "0"
	cpu, err := evaluateRuntimeCandidate(req, cpuOptions, true)
	if err != nil {
		return RuntimePlan{}, err
	}
	if cpu.Fits {
		cpu.Mode = "cpu"
		cpu.RequiresSpillover = true
		return cpu, nil
	}
	return full, nil
}

func planAutomaticMoE(req RuntimePlanRequest, base map[string]string) (RuntimePlan, bool, error) {
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
		if placement.Fits {
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
	if !req.Capabilities.NoKVOffload {
		return RuntimePlan{}, false, nil
	}
	options = cloneStringMap(base)
	if req.Capabilities.CPUMoe {
		options["cpu-moe"] = "true"
	} else {
		options["n-cpu-moe"] = strconv.FormatInt(blocks, 10)
	}
	options["no-kv-offload"] = "true"
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
	if blocks <= 0 {
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
		probe["n-gpu-layers"] = strconv.FormatInt(mid, 10)
		demand := demandFor(req.Demand, probe)
		placement, err := planDemandPlacement(req.Placement, req.Snapshot, demand)
		if err != nil {
			return RuntimePlan{}, false, err
		}
		if placement.Fits {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best <= 0 {
		return RuntimePlan{}, false, nil
	}
	options["n-gpu-layers"] = strconv.FormatInt(best, 10)
	plan, err := evaluateRuntimeCandidate(req, options, true)
	if err != nil {
		return RuntimePlan{}, false, err
	}
	if !plan.Fits {
		return RuntimePlan{}, false, nil
	}
	if moveKV {
		plan.Mode = "hybrid"
	} else {
		plan.Mode = "partial"
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
	return PlanPlacement(snapshot, request)
}

func runtimeHostRAMFits(snapshot hardware.Snapshot, required int64) bool {
	if required <= 0 || snapshot.RAMTotalBytes <= 0 && snapshot.RAMAvailableBytes <= 0 {
		return true
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
	if raw := strings.TrimSpace(optionValue(options, "n-gpu-layers")); raw != "" {
		if layers, err := strconv.ParseInt(raw, 10, 64); err == nil && layers > 0 {
			return "partial"
		}
	}
	if len(placement.Devices) > 1 {
		return "multi_gpu"
	}
	return "full"
}

func hasExplicitOffload(options map[string]string) bool {
	for _, key := range []string{"gpu-layers", "n-gpu-layers", "cpu-moe", "n-cpu-moe", "no-kv-offload"} {
		if _, ok := options[key]; ok {
			return true
		}
		if _, ok := options["--"+key]; ok {
			return true
		}
	}
	return false
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+4)
	for key, value := range in {
		out[key] = value
	}
	return out
}
