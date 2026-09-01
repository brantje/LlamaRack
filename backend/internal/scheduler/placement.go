package scheduler

import (
	"errors"
	"sort"
	"strings"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

const defaultVRAMReserveBytes int64 = 512 * 1024 * 1024

type PlacementRequest struct {
	RequiredBytes int64
	Mode          string
	Devices       []string
	TensorSplit   string
	ReserveBytes  int64
}

type Placement struct {
	Devices        []string `json:"devices"`
	TensorSplit    string   `json:"tensor_split,omitempty"`
	RequiredBytes  int64    `json:"required_bytes"`
	AvailableBytes int64    `json:"available_bytes"`
	Fits           bool     `json:"fits"`
}

// PlanPlacement is deliberately single-GPU first. Automatic placement only
// expands to multiple devices when no single device has enough usable VRAM.
func PlanPlacement(snapshot hardware.Snapshot, request PlacementRequest) (Placement, error) {
	if request.RequiredBytes < 0 {
		return Placement{}, errors.New("required bytes must be zero or greater")
	}
	reserve := request.ReserveBytes
	if reserve <= 0 {
		reserve = defaultVRAMReserveBytes
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "manual" {
		return Placement{}, errors.New("placement mode must be auto or manual")
	}
	if len(snapshot.GPUs) == 0 {
		return Placement{RequiredBytes: request.RequiredBytes, Fits: request.RequiredBytes == 0}, nil
	}

	byID := make(map[string]hardware.GPU, len(snapshot.GPUs))
	for _, gpu := range snapshot.GPUs {
		byID[gpu.ID] = gpu
	}
	if mode == "manual" {
		if len(request.Devices) == 0 {
			return Placement{}, errors.New("manual GPU placement requires at least one device")
		}
		placement := Placement{RequiredBytes: request.RequiredBytes, TensorSplit: strings.TrimSpace(request.TensorSplit)}
		seen := map[string]bool{}
		for _, id := range request.Devices {
			id = strings.TrimSpace(id)
			gpu, ok := byID[id]
			if !ok {
				return Placement{}, errors.New("configured GPU device is not available: " + id)
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			placement.Devices = append(placement.Devices, id)
			placement.AvailableBytes += usableVRAM(gpu, reserve)
		}
		placement.Fits = request.RequiredBytes == 0 || placement.AvailableBytes >= request.RequiredBytes
		return placement, nil
	}

	gpus := append([]hardware.GPU(nil), snapshot.GPUs...)
	sort.Slice(gpus, func(i, j int) bool {
		left, right := usableVRAM(gpus[i], reserve), usableVRAM(gpus[j], reserve)
		if left != right {
			return left > right
		}
		return gpus[i].ID < gpus[j].ID
	})

	// A single adequate device always wins, even if aggregating several devices
	// would have more free memory. This prevents llama.cpp's default spreading.
	for _, gpu := range gpus {
		usable := usableVRAM(gpu, reserve)
		if request.RequiredBytes == 0 || usable >= request.RequiredBytes {
			return Placement{Devices: []string{gpu.ID}, RequiredBytes: request.RequiredBytes, AvailableBytes: usable, Fits: true}, nil
		}
	}

	placement := Placement{RequiredBytes: request.RequiredBytes}
	weights := make([]int64, 0, len(gpus))
	for _, gpu := range gpus {
		usable := usableVRAM(gpu, reserve)
		if usable <= 0 {
			continue
		}
		placement.Devices = append(placement.Devices, gpu.ID)
		placement.AvailableBytes += usable
		weights = append(weights, usable)
		if placement.AvailableBytes >= request.RequiredBytes {
			placement.Fits = true
			break
		}
	}
	if len(placement.Devices) > 1 {
		placement.TensorSplit = tensorSplitFor(weights)
	}
	return placement, nil
}

func usableVRAM(gpu hardware.GPU, reserve int64) int64 {
	usable := gpu.FreeBytes - reserve
	if usable < 0 {
		return 0
	}
	return usable
}

func tensorSplitFor(bytes []int64) string {
	parts := make([]string, 0, len(bytes))
	const unit int64 = 256 * 1024 * 1024
	for _, value := range bytes {
		weight := value / unit
		if weight < 1 {
			weight = 1
		}
		parts = append(parts, intString(weight))
	}
	return strings.Join(parts, ",")
}

func intString(value int64) string {
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
