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
	ID             string  `json:"id"`
	Backend        string  `json:"backend"`
	Index          int     `json:"index"`
	UUID           string  `json:"uuid,omitempty"`
	Name           string  `json:"name"`
	TotalBytes     int64   `json:"total_bytes"`
	UsedBytes      int64   `json:"used_bytes"`
	FreeBytes      int64   `json:"free_bytes"`
	UtilizationPct float64 `json:"utilization_pct"`
}

type GPUProcess struct {
	PID         int    `json:"pid"`
	DeviceID    string `json:"device_id"`
	UsedBytes   int64  `json:"used_bytes"`
	ProcessName string `json:"process_name,omitempty"`
}

type Snapshot struct {
	RAMTotalBytes     int64        `json:"ram_total_bytes"`
	RAMAvailableBytes int64        `json:"ram_available_bytes"`
	GPUs              []GPU        `json:"gpus"`
	Processes         []GPUProcess `json:"processes"`
	CollectedAt       time.Time    `json:"collected_at"`
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
	out, err := d.run(ctx, "nvidia-smi", "--query-gpu=index,uuid,name,memory.total,memory.used,utilization.gpu", "--format=csv,noheader,nounits")
	if err != nil {
		return nil, err
	}
	var gpus []GPU
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
	}
	return gpus, nil
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
		gpus = append(gpus, GPU{ID: "ROCm" + strconv.Itoa(index), Backend: "rocm", Index: index, UUID: uuid, Name: name, TotalBytes: total, UsedBytes: used, FreeBytes: max64(0, total-used), UtilizationPct: util})
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
