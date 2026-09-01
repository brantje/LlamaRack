package hardware

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const mib = int64(1024 * 1024)

type GPU struct {
	ID                            string  `json:"id"`
	Backend                       string  `json:"backend"`
	Index                         int     `json:"index"`
	UUID                          string  `json:"uuid,omitempty"`
	Name                          string  `json:"name"`
	TotalBytes                    int64   `json:"total_bytes"`
	UsedBytes                     int64   `json:"used_bytes"`
	FreeBytes                     int64   `json:"free_bytes"`
	MemoryBandwidthBytesPerSecond int64   `json:"memory_bandwidth_bytes_per_second,omitempty"`
	PCIeBandwidthBytesPerSecond   int64   `json:"pcie_bandwidth_bytes_per_second,omitempty"`
	UtilizationPct                float64 `json:"utilization_pct"`
}

type GPUProcess struct {
	PID         int    `json:"pid"`
	DeviceID    string `json:"device_id"`
	UsedBytes   int64  `json:"used_bytes"`
	ProcessName string `json:"process_name,omitempty"`
}

type Snapshot struct {
	RAMTotalBytes              int64        `json:"ram_total_bytes"`
	RAMAvailableBytes          int64        `json:"ram_available_bytes"`
	RAMBandwidthBytesPerSecond int64        `json:"ram_bandwidth_bytes_per_second,omitempty"`
	GPUs                       []GPU        `json:"gpus"`
	Processes                  []GPUProcess `json:"processes"`
	CollectedAt                time.Time    `json:"collected_at"`
}

type Snapshotter interface {
	Snapshot(context.Context) (Snapshot, error)
}

type runner func(context.Context, string, ...string) ([]byte, error)

type Detector struct {
	run      runner
	readFile func(string) ([]byte, error)
	now      func() time.Time
}

func New() *Detector {
	return &Detector{
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		readFile: os.ReadFile,
		now:      time.Now,
	}
}

func (d *Detector) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot := Snapshot{CollectedAt: d.now().UTC(), GPUs: []GPU{}, Processes: []GPUProcess{}}
	if data, err := d.readFile("/proc/meminfo"); err == nil {
		snapshot.RAMTotalBytes, snapshot.RAMAvailableBytes = parseMemInfo(string(data))
		if snapshot.RAMTotalBytes > 0 {
			snapshot.RAMBandwidthBytesPerSecond = hostMemoryBandwidth()
		}
	}

	var probeErrors []error
	if gpus, err := d.nvidiaGPUs(ctx); err == nil {
		snapshot.GPUs = append(snapshot.GPUs, gpus...)
	} else if !isCommandUnavailable(err) {
		probeErrors = append(probeErrors, err)
	}
	if gpus, err := d.rocmGPUs(ctx); err == nil {
		snapshot.GPUs = append(snapshot.GPUs, gpus...)
	} else if !isCommandUnavailable(err) {
		probeErrors = append(probeErrors, err)
	}
	if processes, err := d.nvidiaProcesses(ctx, snapshot.GPUs); err == nil {
		snapshot.Processes = append(snapshot.Processes, processes...)
	} else if !isCommandUnavailable(err) {
		probeErrors = append(probeErrors, err)
	}

	sort.Slice(snapshot.GPUs, func(i, j int) bool {
		if snapshot.GPUs[i].Backend != snapshot.GPUs[j].Backend {
			return snapshot.GPUs[i].Backend < snapshot.GPUs[j].Backend
		}
		return snapshot.GPUs[i].Index < snapshot.GPUs[j].Index
	})
	if len(snapshot.GPUs) == 0 && len(probeErrors) > 0 {
		return snapshot, errors.Join(probeErrors...)
	}
	return snapshot, nil
}

func parseMemInfo(text string) (total, available int64) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, _ := strconv.ParseInt(fields[1], 10, 64)
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			total = value * 1024
		case "MemAvailable":
			available = value * 1024
		}
	}
	return total, available
}

func (d *Detector) nvidiaGPUs(ctx context.Context) ([]GPU, error) {
	// clocks.max.memory is optional enrichment used to derive the theoretical
	// VRAM bandwidth. Fall back to the older query on drivers that do not expose
	// this property so bandwidth telemetry can never break GPU detection.
	out, err := d.run(ctx, "nvidia-smi", "--query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu,clocks.max.memory", "--format=csv,noheader,nounits")
	withMemoryClock := err == nil
	if err != nil {
		out, err = d.run(ctx, "nvidia-smi", "--query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu", "--format=csv,noheader,nounits")
		if err != nil {
			return nil, err
		}
	}

	var gpus []GPU
	memoryClocksMHz := make([]float64, 0)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := splitCSV(line)
		if len(parts) < 6 {
			continue
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		totalMiB, _ := strconv.ParseInt(parts[3], 10, 64)
		usedMiB, _ := strconv.ParseInt(parts[4], 10, 64)
		util, _ := strconv.ParseFloat(parts[5], 64)
		total, used := totalMiB*mib, usedMiB*mib
		gpus = append(gpus, GPU{ID: "CUDA" + strconv.Itoa(index), Backend: "cuda", Index: index, UUID: parts[1], Name: parts[2], TotalBytes: total, UsedBytes: used, FreeBytes: max64(0, total-used), UtilizationPct: util})
		clock := float64(0)
		if withMemoryClock && len(parts) > 6 {
			clock, _ = strconv.ParseFloat(parts[6], 64)
		}
		memoryClocksMHz = append(memoryClocksMHz, clock)
	}

	// nvidia-smi's selective query exposes the max memory clock but not the bus
	// width consistently across driver generations. The regular -q report does;
	// parse those widths in device order and combine both facts. This enrichment
	// is deliberately best-effort and never turns a healthy GPU probe into an
	// error if a driver omits the field.
	if len(gpus) > 0 {
		if query, queryErr := d.run(ctx, "nvidia-smi", "-q"); queryErr == nil {
			widths := parseNVIDIAMemoryBusWidths(string(query))
			if len(widths) == len(gpus) && len(memoryClocksMHz) == len(gpus) {
				for i := range gpus {
					gpus[i].MemoryBandwidthBytesPerSecond = theoreticalMemoryBandwidth(memoryClocksMHz[i], widths[i])
				}
			}
		}
		d.enrichNVIDIAPCIe(ctx, gpus)
	}
	return gpus, nil
}

func parseNVIDIAMemoryBusWidths(text string) []float64 {
	var widths []float64
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(strings.ToLower(line), "memory bus width") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(parts[1]))
		if len(fields) == 0 {
			continue
		}
		width, err := strconv.ParseFloat(fields[0], 64)
		if err == nil && width > 0 {
			widths = append(widths, width)
		}
	}
	return widths
}

func theoreticalMemoryBandwidth(memoryClockMHz, busWidthBits float64) int64 {
	if memoryClockMHz <= 0 || busWidthBits <= 0 {
		return 0
	}
	// NVML/nvidia-smi reports the GDDR memory clock at half the effective data
	// transfer rate. DDR transfers on both clock edges, hence the factor of two.
	return int64(memoryClockMHz * 1_000_000 * 2 * busWidthBits / 8)
}

func (d *Detector) rocmGPUs(ctx context.Context) ([]GPU, error) {
	out, err := d.run(ctx, "rocm-smi", "--showproductname", "--showuniqueid", "--showmeminfo", "vram", "--showuse", "--json")
	if err != nil {
		return nil, err
	}
	var cards map[string]map[string]any
	if err := json.Unmarshal(out, &cards); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(cards))
	for key := range cards {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var gpus []GPU
	for _, key := range keys {
		card := cards[key]
		index := trailingIndex(key, len(gpus))
		total := int64Value(findValue(card, "VRAM Total Memory (B)"))
		used := int64Value(findValue(card, "VRAM Total Used Memory (B)"))
		name := stringValue(firstValue(card, "Card series", "Card model", "Card SKU", "Device Name"))
		if name == "" {
			name = "AMD GPU " + strconv.Itoa(index)
		}
		uuid := stringValue(firstValue(card, "Unique ID", "Unique ID (Hex)", "GPU ID"))
		util := float64Value(findValue(card, "GPU use (%)"))
		bandwidth := int64Value(firstValue(card, "Memory Bandwidth (B/s)", "VRAM Memory Bandwidth (B/s)"))
		pcieBandwidth := d.rocmPCIeBandwidth(index)
		gpus = append(gpus, GPU{ID: "ROCm" + strconv.Itoa(index), Backend: "rocm", Index: index, UUID: uuid, Name: name, TotalBytes: total, UsedBytes: used, FreeBytes: max64(0, total-used), MemoryBandwidthBytesPerSecond: bandwidth, PCIeBandwidthBytesPerSecond: pcieBandwidth, UtilizationPct: util})
	}
	return gpus, nil
}

func (d *Detector) nvidiaProcesses(ctx context.Context, gpus []GPU) ([]GPUProcess, error) {
	uuidToID := map[string]string{}
	for _, gpu := range gpus {
		if gpu.Backend == "cuda" && gpu.UUID != "" {
			uuidToID[gpu.UUID] = gpu.ID
		}
	}
	if len(uuidToID) == 0 {
		return nil, nil
	}
	out, err := d.run(ctx, "nvidia-smi", "--query-compute-apps=pid,gpu_uuid,used_memory,process_name", "--format=csv,noheader,nounits")
	if err != nil {
		return nil, err
	}
	var processes []GPUProcess
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := splitCSV(line)
		if len(parts) < 3 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		deviceID := uuidToID[parts[1]]
		if deviceID == "" {
			continue
		}
		usedMiB, _ := strconv.ParseInt(parts[2], 10, 64)
		process := GPUProcess{PID: pid, DeviceID: deviceID, UsedBytes: usedMiB * mib}
		if len(parts) > 3 {
			process.ProcessName = parts[3]
		}
		processes = append(processes, process)
	}
	return processes, nil
}

func splitCSV(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func trailingIndex(value string, fallback int) int {
	value = strings.TrimSpace(value)
	start := len(value)
	for start > 0 && value[start-1] >= '0' && value[start-1] <= '9' {
		start--
	}
	if start == len(value) {
		return fallback
	}
	index, err := strconv.Atoi(value[start:])
	if err != nil {
		return fallback
	}
	return index
}

func firstValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value := findValue(values, key); value != nil {
			return value
		}
	}
	return nil
}

func findValue(values map[string]any, key string) any {
	for candidate, value := range values {
		if strings.EqualFold(strings.TrimSpace(candidate), key) {
			return value
		}
	}
	return nil
}

func int64Value(value any) int64 {
	switch value := value.(type) {
	case float64:
		return int64(value)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed
	default:
		return 0
	}
}

func float64Value(value any) float64 {
	switch value := value.(type) {
	case float64:
		return value
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "%")), 64)
		return parsed
	default:
		return 0
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func isCommandUnavailable(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr) || errors.Is(err, os.ErrNotExist)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
