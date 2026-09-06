package scheduler

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

// GPUResource is reclaimable or required capacity on one device.
type GPUResource struct {
	DeviceID string
	Bytes    int64
}

// CandidateResources is the per-device (and host RAM) vector a loaded
// instance would release if stopped.
type CandidateResources struct {
	HostRAMBytes int64
	GPU          []GPUResource
}

// Candidate describes a currently loaded instance that could potentially be
// stopped to free resources for another model.
type Candidate struct {
	ModelID         string
	InstanceID      string
	Priority        string
	AlwaysOn        bool
	EvictionEnabled bool
	ActiveRequests  int
	LastUsed        time.Time
	EstimatedBytes  int64
	Resources       CandidateResources
	Ready           bool
}

// Plan is a deterministic eviction decision for a requested placement.
// Fits is false when the eligible candidates cannot free enough on the devices
// the placement actually uses.
type Plan struct {
	Evict      []Candidate
	FreedBytes int64
	Freed      []GPUResource
	Devices    []string
	Fits       bool
}

// ResourceAttribution is the input used to attach reclaimable VRAM to a
// candidate without inventing a device identity.
type ResourceAttribution struct {
	EstimatedBytes int64
	HostRAMBytes   int64
	Devices        []string
	TensorSplit    string
	LeaseGPUs      []GPUReservation
	PID            int
	Processes      []hardware.GPUProcess
	SnapshotGPUs   []hardware.GPU
}

// RankEvictionCandidates returns only normally evictable instances ordered by
// priority first (Low before Normal before High), then least-recently-used,
// then largest estimated resource release, with stable ID tie-breakers.
// Always On expresses desired lifecycle state and does not affect normal
// resource-pressure eviction eligibility; EvictionEnabled is the source of
// truth for that policy.
func RankEvictionCandidates(candidates []Candidate) []Candidate {
	out := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.Ready || !candidate.EvictionEnabled || candidate.ActiveRequests > 0 {
			continue
		}
		out = append(out, candidate)
	}

	sort.Slice(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if lp, rp := priorityRank(left.Priority), priorityRank(right.Priority); lp != rp {
			return lp < rp
		}
		if !left.LastUsed.Equal(right.LastUsed) {
			if left.LastUsed.IsZero() {
				return true
			}
			if right.LastUsed.IsZero() {
				return false
			}
			return left.LastUsed.Before(right.LastUsed)
		}
		if left.EstimatedBytes != right.EstimatedBytes {
			return left.EstimatedBytes > right.EstimatedBytes
		}
		if left.ModelID != right.ModelID {
			return left.ModelID < right.ModelID
		}
		return left.InstanceID < right.InstanceID
	})
	return out
}

// PlanEvictions chooses the smallest ranked victim set that makes request fit
// on the devices the placement actually uses. Automatic placement stays
// single-GPU first: each device is tried in the same largest-usable order as
// PlanPlacement before any multi-GPU aggregation.
func PlanEvictions(candidates []Candidate, snapshot hardware.Snapshot, request PlacementRequest) Plan {
	if request.RequiredBytes <= 0 {
		return Plan{Fits: true}
	}
	if len(snapshot.GPUs) == 0 {
		return PlanEvictionsBytes(candidates, request.RequiredBytes)
	}

	if current, err := PlanPlacement(snapshot, request); err == nil && current.Fits {
		return Plan{Fits: true, Devices: append([]string(nil), current.Devices...)}
	}

	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = "auto"
	}
	if mode == "manual" {
		return coverPlacement(candidates, snapshot, request)
	}

	reserve := requestReserve(request)
	for _, gpu := range sortedGPUsByUsable(snapshot, reserve) {
		single := PlacementRequest{
			RequiredBytes: request.RequiredBytes,
			Mode:          "manual",
			Devices:       []string{gpu.ID},
			ReserveBytes:  request.ReserveBytes,
		}
		plan := coverPlacement(candidates, snapshot, single)
		if plan.Fits {
			return plan
		}
	}
	return coverPlacement(candidates, snapshot, request)
}

// PlanEvictionsBytes covers a scalar byte shortfall. It is only used when no
// GPU inventory is available (CPU/other-backend hosts and eligibility previews).
func PlanEvictionsBytes(candidates []Candidate, requiredBytes int64) Plan {
	if requiredBytes <= 0 {
		return Plan{Fits: true}
	}

	plan := Plan{}
	for _, candidate := range RankEvictionCandidates(candidates) {
		plan.Evict = append(plan.Evict, candidate)
		released := candidateGPUBytes(candidate)
		if released <= 0 {
			released = candidate.EstimatedBytes
		}
		if released > 0 {
			plan.FreedBytes += released
		}
		if plan.FreedBytes >= requiredBytes {
			plan.Fits = true
			return plan
		}
	}
	return plan
}

func coverPlacement(candidates []Candidate, snapshot hardware.Snapshot, request PlacementRequest) Plan {
	current, err := PlanPlacement(snapshot, request)
	if err != nil {
		return Plan{}
	}
	if current.Fits {
		return planFromSelection(nil, current)
	}

	selected := make([]Candidate, 0, len(candidates))
	for _, candidate := range RankEvictionCandidates(candidates) {
		if candidateGPUBytes(candidate) <= 0 {
			continue
		}
		trial := append(append([]Candidate(nil), selected...), candidate)
		after, err := PlanPlacement(snapshotWithCandidateCredits(snapshot, trial), request)
		if err != nil {
			continue
		}
		if !after.Fits && after.AvailableBytes <= current.AvailableBytes {
			continue
		}
		selected = append(selected, candidate)
		current = after
		if after.Fits {
			return planFromSelection(selected, after)
		}
	}
	return planFromSelection(selected, current)
}

func planFromSelection(selected []Candidate, placement Placement) Plan {
	target := map[string]bool{}
	for _, id := range placement.Devices {
		target[id] = true
	}
	if len(target) == 0 {
		for _, candidate := range selected {
			for _, gpu := range candidate.Resources.GPU {
				if strings.TrimSpace(gpu.DeviceID) != "" {
					target[gpu.DeviceID] = true
				}
			}
		}
	}
	freedByDevice := map[string]int64{}
	order := make([]string, 0, len(target))
	total := int64(0)
	for _, candidate := range selected {
		for _, gpu := range candidate.Resources.GPU {
			id := strings.TrimSpace(gpu.DeviceID)
			if id == "" || gpu.Bytes <= 0 {
				continue
			}
			if len(placement.Devices) > 0 && !target[id] {
				continue
			}
			if _, ok := freedByDevice[id]; !ok {
				order = append(order, id)
			}
			freedByDevice[id] += gpu.Bytes
			total += gpu.Bytes
		}
	}
	freed := make([]GPUResource, 0, len(order))
	for _, id := range order {
		freed = append(freed, GPUResource{DeviceID: id, Bytes: freedByDevice[id]})
	}
	return Plan{
		Evict:      selected,
		FreedBytes: total,
		Freed:      freed,
		Devices:    append([]string(nil), placement.Devices...),
		Fits:       placement.Fits,
	}
}

func snapshotWithCandidateCredits(snapshot hardware.Snapshot, selected []Candidate) hardware.Snapshot {
	return adjustSnapshot(snapshot, nil, creditBytesFromCandidates(selected))
}

// ApplyCandidateCredits pretends selected instances have already released
// their attributed per-device VRAM. Used to re-plan after a stop when the
// hardware snapshot has not yet observed the free memory.
func ApplyCandidateCredits(snapshot hardware.Snapshot, selected []Candidate) hardware.Snapshot {
	return snapshotWithCandidateCredits(snapshot, selected)
}

func creditBytesFromCandidates(selected []Candidate) map[string]int64 {
	out := map[string]int64{}
	for _, candidate := range selected {
		for _, gpu := range candidate.Resources.GPU {
			id := strings.TrimSpace(gpu.DeviceID)
			if id == "" || gpu.Bytes <= 0 {
				continue
			}
			out[id] += gpu.Bytes
		}
	}
	return out
}

// CreditsFromCandidates builds per-device ledger credits. Scalar Bytes is
// left zero so leftover estimates cannot be dumped onto an unrelated GPU.
func CreditsFromCandidates(candidates []Candidate) []Credit {
	out := make([]Credit, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.InstanceID) == "" {
			continue
		}
		credit := Credit{InstanceID: candidate.InstanceID}
		for _, gpu := range candidate.Resources.GPU {
			id := strings.TrimSpace(gpu.DeviceID)
			if id == "" || gpu.Bytes <= 0 {
				continue
			}
			credit.GPUs = append(credit.GPUs, GPUReservation{DeviceID: id, Bytes: gpu.Bytes})
		}
		out = append(out, credit)
	}
	return out
}

func candidateGPUBytes(candidate Candidate) int64 {
	total := int64(0)
	for _, gpu := range candidate.Resources.GPU {
		if strings.TrimSpace(gpu.DeviceID) == "" || gpu.Bytes <= 0 {
			continue
		}
		total += gpu.Bytes
	}
	return total
}

// AttributeResources fills a per-device resource vector. Observed process VRAM
// wins per device, then missing lease/configured devices are filled from the
// lease or a split of the estimate. Tensor-split workers often report nvidia-smi
// used-memory on only one GPU; the lease still names every reserved device.
// Unknown devices are not invented except when the snapshot contains exactly
// one GPU, which is the only device the instance could be using.
func AttributeResources(in ResourceAttribution) CandidateResources {
	out := CandidateResources{HostRAMBytes: in.HostRAMBytes}
	devices := reservedDeviceIDs(in.Devices, in.LeaseGPUs)
	observed := observedProcessGPUs(in.PID, in.Processes)
	if len(observed) > 0 {
		out.GPU = unionObservedWithReserved(observed, devices, in.LeaseGPUs, in.EstimatedBytes, in.TensorSplit)
		return out
	}
	if len(devices) > 0 {
		if in.EstimatedBytes > 0 {
			out.GPU = SplitEstimateAcrossDevices(in.EstimatedBytes, devices, in.TensorSplit)
			return out
		}
		out.GPU = gpuResourcesFromLease(in.LeaseGPUs)
		return out
	}
	if len(in.SnapshotGPUs) == 1 {
		id := strings.TrimSpace(in.SnapshotGPUs[0].ID)
		if id != "" && in.EstimatedBytes > 0 {
			out.GPU = []GPUResource{{DeviceID: id, Bytes: in.EstimatedBytes}}
		}
	}
	return out
}

func reservedDeviceIDs(configured []string, lease []GPUReservation) []string {
	devices := cleanDeviceIDs(configured)
	if len(devices) > 0 {
		return devices
	}
	for _, gpu := range lease {
		id := strings.TrimSpace(gpu.DeviceID)
		if id != "" {
			devices = append(devices, id)
		}
	}
	return cleanDeviceIDs(devices)
}

func unionObservedWithReserved(observed []GPUResource, devices []string, lease []GPUReservation, estimated int64, tensorSplit string) []GPUResource {
	out := append([]GPUResource(nil), observed...)
	have := map[string]bool{}
	for _, gpu := range out {
		have[gpu.DeviceID] = true
	}
	leaseByID := map[string]int64{}
	for _, gpu := range lease {
		id := strings.TrimSpace(gpu.DeviceID)
		if id == "" || gpu.Bytes <= 0 {
			continue
		}
		leaseByID[id] = gpu.Bytes
		if !have[id] {
			devices = append(devices, id)
		}
	}
	devices = cleanDeviceIDs(devices)
	leaseAllocated := int64(0)
	missing := make([]string, 0, len(devices))
	for _, id := range devices {
		if have[id] {
			continue
		}
		if bytes := leaseByID[id]; bytes > 0 {
			out = append(out, GPUResource{DeviceID: id, Bytes: bytes})
			leaseAllocated += bytes
			have[id] = true
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) == 0 || estimated <= 0 {
		return out
	}
	observedTotal := int64(0)
	for _, gpu := range observed {
		if gpu.Bytes > 0 {
			observedTotal += gpu.Bytes
		}
	}
	remaining := estimated - observedTotal - leaseAllocated
	if remaining < 1 {
		return out
	}
	out = append(out, SplitEstimateAcrossDevices(remaining, missing, tensorSplit)...)
	return out
}

func observedProcessGPUs(pid int, processes []hardware.GPUProcess) []GPUResource {
	if pid <= 0 {
		return nil
	}
	bytesByDevice := map[string]int64{}
	order := make([]string, 0, 2)
	for _, process := range processes {
		if process.PID != pid {
			continue
		}
		id := strings.TrimSpace(process.DeviceID)
		if id == "" || process.UsedBytes <= 0 {
			continue
		}
		if _, ok := bytesByDevice[id]; !ok {
			order = append(order, id)
		}
		bytesByDevice[id] += process.UsedBytes
	}
	if len(order) == 0 {
		return nil
	}
	out := make([]GPUResource, 0, len(order))
	for _, id := range order {
		out = append(out, GPUResource{DeviceID: id, Bytes: bytesByDevice[id]})
	}
	return out
}

func gpuResourcesFromLease(lease []GPUReservation) []GPUResource {
	out := make([]GPUResource, 0, len(lease))
	for _, gpu := range lease {
		id := strings.TrimSpace(gpu.DeviceID)
		if id == "" || gpu.Bytes <= 0 {
			continue
		}
		out = append(out, GPUResource{DeviceID: id, Bytes: gpu.Bytes})
	}
	return out
}

// SplitEstimateAcrossDevices spreads total bytes across devices using
// tensor-split weights, or evenly when no usable split is present.
func SplitEstimateAcrossDevices(total int64, devices []string, tensorSplit string) []GPUResource {
	devices = cleanDeviceIDs(devices)
	if total <= 0 || len(devices) == 0 {
		return nil
	}
	if len(devices) == 1 {
		return []GPUResource{{DeviceID: devices[0], Bytes: total}}
	}
	weights := tensorSplitWeights(tensorSplit, len(devices))
	sum := 0.0
	for _, weight := range weights {
		sum += weight
	}
	if sum <= 0 {
		weights = make([]float64, len(devices))
		for i := range weights {
			weights[i] = 1
		}
		sum = float64(len(devices))
	}
	out := make([]GPUResource, len(devices))
	remaining := total
	for i, id := range devices {
		var bytes int64
		if i == len(devices)-1 {
			bytes = remaining
		} else {
			bytes = int64(float64(total) * weights[i] / sum)
			remaining -= bytes
		}
		out[i] = GPUResource{DeviceID: id, Bytes: bytes}
	}
	return out
}

func tensorSplitWeights(tensorSplit string, count int) []float64 {
	parts := strings.Split(tensorSplit, ",")
	weights := make([]float64, 0, count)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil
		}
		weights = append(weights, value)
	}
	if len(weights) != count {
		return nil
	}
	return weights
}

func cleanDeviceIDs(devices []string) []string {
	out := make([]string, 0, len(devices))
	seen := map[string]bool{}
	for _, id := range devices {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func sortedGPUsByUsable(snapshot hardware.Snapshot, reserve int64) []hardware.GPU {
	gpus := append([]hardware.GPU(nil), snapshot.GPUs...)
	sort.Slice(gpus, func(i, j int) bool {
		left, right := usableVRAM(gpus[i], reserve), usableVRAM(gpus[j], reserve)
		if left != right {
			return left > right
		}
		return gpus[i].ID < gpus[j].ID
	})
	return gpus
}

func requestReserve(request PlacementRequest) int64 {
	if request.ReserveBytes > 0 {
		return request.ReserveBytes
	}
	return defaultVRAMReserveBytes
}

func priorityRank(priority string) int {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low":
		return 0
	case "high":
		return 2
	default:
		return 1
	}
}
