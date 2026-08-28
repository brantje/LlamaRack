package hardware

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	hostMemoryBandwidthOnce   sync.Once
	cachedHostMemoryBandwidth int64
)

// hostMemoryBandwidth returns a process-lifetime measurement of effective
// sequential host-memory copy throughput. It is intentionally measured once:
// hardware snapshots are frequent, while this benchmark exists only to give
// the Discover speed model a machine-specific lower-bound style signal.
func hostMemoryBandwidth() int64 {
	hostMemoryBandwidthOnce.Do(func() {
		cachedHostMemoryBandwidth = benchmarkHostMemoryBandwidth()
	})
	return cachedHostMemoryBandwidth
}

func benchmarkHostMemoryBandwidth() int64 {
	const bufferBytes = 64 * 1024 * 1024
	const iterations = 6

	source := make([]byte, bufferBytes)
	target := make([]byte, bufferBytes)
	for i := 0; i < len(source); i += 4096 {
		source[i] = byte(i)
	}

	// Warm the copy path once, then time enough traffic to reduce timer noise.
	copy(target, source)
	start := time.Now()
	var copied int64
	for i := 0; i < iterations; i++ {
		copy(target, source)
		copied += int64(len(source))
		source, target = target, source
	}
	elapsed := time.Since(start)
	runtime.KeepAlive(source)
	runtime.KeepAlive(target)
	if elapsed <= 0 || copied <= 0 {
		return 0
	}
	return int64(float64(copied) / elapsed.Seconds())
}

// enrichNVIDIAPCIe is the final best-effort NVIDIA enrichment pass. Keep every
// optional property in its own query: consumer drivers and containerized NVML
// installations frequently expose some fields but not others. One unsupported
// property must never discard otherwise usable bandwidth telemetry.
func (d *Detector) enrichNVIDIAPCIe(ctx context.Context, gpus []GPU) {
	d.enrichNVIDIAMemoryBandwidth(ctx, gpus)

	if out, err := d.run(ctx, "nvidia-smi", "--query-gpu=index,pcie.link.gen.max,pcie.link.width.max", "--format=csv,noheader,nounits"); err == nil {
		byIndex := gpuPositionsByIndex(gpus)
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := splitCSV(line)
			if len(parts) < 3 {
				continue
			}
			index, errIndex := strconv.Atoi(parts[0])
			generation, errGeneration := strconv.Atoi(parts[1])
			width, errWidth := strconv.Atoi(parts[2])
			position, ok := byIndex[index]
			if !ok || errIndex != nil || errGeneration != nil || errWidth != nil {
				continue
			}
			gpus[position].PCIeBandwidthBytesPerSecond = theoreticalPCIeBandwidth(generation, width)
		}
	}

	// nvidia-smi can expose a GPU while hiding PCIe max-link properties inside a
	// container. Linux sysfs normally still exposes the physical PCI function,
	// so use it only for devices whose NVML PCIe query remained unavailable.
	busIDs := d.nvidiaPCIBusIDs(ctx)
	for i := range gpus {
		if gpus[i].PCIeBandwidthBytesPerSecond > 0 {
			continue
		}
		if busID := busIDs[gpus[i].Index]; busID != "" {
			gpus[i].PCIeBandwidthBytesPerSecond = d.pcieBandwidthFromSysfs(busID)
		}
	}
}

func (d *Detector) enrichNVIDIAMemoryBandwidth(ctx context.Context, gpus []GPU) {
	needsBandwidth := false
	for _, gpu := range gpus {
		if gpu.MemoryBandwidthBytesPerSecond <= 0 {
			needsBandwidth = true
			break
		}
	}
	if !needsBandwidth {
		return
	}

	// nvmlDeviceGetMemoryBusWidth has existed since the R510 driver family and
	// modern nvidia-smi exposes it as memory.bus_width. Query it directly instead
	// of relying on the human-readable `nvidia-smi -q` layout, which is not
	// consistent across GeForce/container driver combinations.
	widths := d.nvidiaFloatMetric(ctx, "memory.bus_width")
	clocks := d.nvidiaFloatMetric(ctx, "clocks.max.memory")
	for i := range gpus {
		if gpus[i].MemoryBandwidthBytesPerSecond > 0 {
			continue
		}
		gpus[i].MemoryBandwidthBytesPerSecond = theoreticalMemoryBandwidth(clocks[gpus[i].Index], widths[gpus[i].Index])
	}
}

func (d *Detector) nvidiaFloatMetric(ctx context.Context, field string) map[int]float64 {
	values := map[int]float64{}
	out, err := d.run(ctx, "nvidia-smi", "--query-gpu=index,"+field, "--format=csv,noheader,nounits")
	if err != nil {
		return values
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := splitCSV(line)
		if len(parts) < 2 {
			continue
		}
		index, errIndex := strconv.Atoi(parts[0])
		fields := strings.Fields(parts[1])
		if errIndex != nil || len(fields) == 0 {
			continue
		}
		value, errValue := strconv.ParseFloat(fields[0], 64)
		if errValue == nil && value > 0 {
			values[index] = value
		}
	}
	return values
}

func gpuPositionsByIndex(gpus []GPU) map[int]int {
	byIndex := make(map[int]int, len(gpus))
	for i := range gpus {
		byIndex[gpus[i].Index] = i
	}
	return byIndex
}

func (d *Detector) nvidiaPCIBusIDs(ctx context.Context) map[int]string {
	values := map[int]string{}
	out, err := d.run(ctx, "nvidia-smi", "--query-gpu=index,pci.bus_id", "--format=csv,noheader,nounits")
	if err != nil {
		return values
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := splitCSV(line)
		if len(parts) < 2 {
			continue
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		if busID := normalizePCIBusID(parts[1]); busID != "" {
			values[index] = busID
		}
	}
	return values
}

func normalizePCIBusID(value string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(value)), ":")
	if len(parts) != 3 {
		return ""
	}
	if len(parts[0]) > 4 {
		parts[0] = parts[0][len(parts[0])-4:]
	}
	if len(parts[0]) != 4 || parts[1] == "" || parts[2] == "" {
		return ""
	}
	return strings.Join(parts, ":")
}

func (d *Detector) pcieBandwidthFromSysfs(busID string) int64 {
	speedData, speedErr := d.readFile("/sys/bus/pci/devices/" + busID + "/max_link_speed")
	widthData, widthErr := d.readFile("/sys/bus/pci/devices/" + busID + "/max_link_width")
	if speedErr != nil || widthErr != nil {
		return 0
	}
	transfers := parsePCIeTransfersPerSecond(string(speedData))
	width, err := strconv.Atoi(strings.TrimSpace(string(widthData)))
	if err != nil || width <= 0 {
		return 0
	}
	return pcieBandwidthForTransfers(transfers, width)
}

func (d *Detector) rocmPCIeBandwidth(index int) int64 {
	speedPath := fmt.Sprintf("/sys/class/drm/card%d/device/max_link_speed", index)
	widthPath := fmt.Sprintf("/sys/class/drm/card%d/device/max_link_width", index)
	speedData, speedErr := d.readFile(speedPath)
	widthData, widthErr := d.readFile(widthPath)
	if speedErr != nil || widthErr != nil {
		return 0
	}
	transfers := parsePCIeTransfersPerSecond(string(speedData))
	width, err := strconv.Atoi(strings.TrimSpace(string(widthData)))
	if err != nil || width <= 0 {
		return 0
	}
	return pcieBandwidthForTransfers(transfers, width)
}

func parsePCIeTransfersPerSecond(value string) float64 {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return 0
	}
	transfers, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || transfers <= 0 {
		return 0
	}
	return transfers
}

func theoreticalPCIeBandwidth(generation, width int) int64 {
	if generation <= 0 || width <= 0 {
		return 0
	}
	transfers := map[int]float64{
		1: 2.5,
		2: 5,
		3: 8,
		4: 16,
		5: 32,
		6: 64,
		7: 128,
	}[generation]
	return pcieBandwidthForTransfers(transfers, width)
}

func pcieBandwidthForTransfers(gigaTransfersPerSecond float64, width int) int64 {
	if gigaTransfersPerSecond <= 0 || width <= 0 {
		return 0
	}
	encodingEfficiency := 128.0 / 130.0
	if gigaTransfersPerSecond <= 5 {
		encodingEfficiency = 0.8 // PCIe 1.x/2.x use 8b/10b encoding.
	}
	return int64(gigaTransfersPerSecond * 1_000_000_000 * encodingEfficiency * float64(width) / 8)
}
