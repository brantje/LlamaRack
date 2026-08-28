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
	hostMemoryBandwidthOnce sync.Once
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

// enrichNVIDIAPCIe keeps PCIe telemetry optional so an older driver can never
// turn a valid GPU inventory into a failed snapshot.
func (d *Detector) enrichNVIDIAPCIe(ctx context.Context, gpus []GPU) {
	out, err := d.run(ctx, "nvidia-smi", "--query-gpu=index,pcie.link.gen.max,pcie.link.width.max", "--format=csv,noheader,nounits")
	if err != nil {
		return
	}
	byIndex := make(map[int]int, len(gpus))
	for i := range gpus {
		byIndex[gpus[i].Index] = i
	}
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
