package telemetry

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
	"github.com/brantje/llamacpp-manager/backend/internal/systemlog"
)

type GPUUsage struct {
	DeviceID       string   `json:"device_id"`
	VRAMUsedBytes  *int64   `json:"vram_used_bytes,omitempty"`
	UtilizationPct *float64 `json:"utilization_pct,omitempty"`
}

type Sample struct {
	InstanceID        string     `json:"instance_id"`
	PID               int        `json:"pid"`
	GPUDevices        []string   `json:"gpu_devices"`
	GPUs              []GPUUsage `json:"gpus"`
	VRAMUsedBytes     *int64     `json:"vram_used_bytes,omitempty"`
	GPUUtilizationPct *float64   `json:"gpu_utilization_pct,omitempty"`
	CPUPercent        *float64   `json:"cpu_percent,omitempty"`
	MemoryUsedBytes   *int64     `json:"memory_used_bytes,omitempty"`
	CollectedAt       time.Time  `json:"collected_at"`
}

type snapshotFunc func(context.Context) (hardware.Snapshot, error)
type runner func(context.Context, string, ...string) ([]byte, error)
type readFileFunc func(string) ([]byte, error)

type cpuPoint struct {
	processTicks uint64
	systemTicks  uint64
}

type amdPoint struct {
	gfxNS uint64
	at    time.Time
}

type amdProcess struct {
	pid         int
	deviceID    string
	vramBytes   int64
	utilization *float64
}

type Collector struct {
	snapshot     snapshotFunc
	run          runner
	readFile     readFileFunc
	now          func() time.Time
	numCPU       int
	hostProcRoot string
	cpuPrev      map[int]cpuPoint
	amdPrev      map[string]amdPoint
}

func New(snapshot func(context.Context) (hardware.Snapshot, error)) *Collector {
	return &Collector{
		snapshot: snapshot,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		readFile:     os.ReadFile,
		now:          time.Now,
		numCPU:       runtime.NumCPU(),
		hostProcRoot: strings.TrimSpace(os.Getenv("LCM_HOST_PROC")),
		cpuPrev:      map[int]cpuPoint{},
		amdPrev:      map[string]amdPoint{},
	}
}

func (c *Collector) Collect(ctx context.Context, runtimes []supervisor.Runtime) []Sample {
	active := make([]supervisor.Runtime, 0, len(runtimes))
	activePIDs := make(map[int]struct{}, len(runtimes))
	for _, item := range runtimes {
		if item.PID <= 0 {
			continue
		}
		active = append(active, item)
		activePIDs[item.PID] = struct{}{}
	}
	if len(active) == 0 {
		c.cpuPrev = map[int]cpuPoint{}
		c.amdPrev = map[string]amdPoint{}
		return nil
	}

	collectedAt := c.now().UTC()
	var snapshot hardware.Snapshot
	if c.snapshot != nil {
		snapshot, _ = c.snapshot(ctx)
	}
	nvidiaUtil := c.nvidiaProcessUtilization(ctx)
	amdProcesses := c.amdProcesses(ctx, collectedAt)
	resolveReportedPID := c.runtimePIDResolver(activePIDs)
	mappedDiagnostics := map[string]struct{}{}
	logPIDMapping := func(reportedPID, resolvedPID int, deviceID string) {
		if reportedPID <= 0 || resolvedPID <= 0 || reportedPID == resolvedPID || strings.TrimSpace(deviceID) == "" {
			return
		}
		key := fmt.Sprintf("%d:%d:%s", reportedPID, resolvedPID, deviceID)
		if _, exists := mappedDiagnostics[key]; exists {
			return
		}
		mappedDiagnostics[key] = struct{}{}
		systemlog.Log(systemlog.Debug, "telemetry", fmt.Sprintf("NSpid map: host %d -> container %d (%s)", reportedPID, resolvedPID, deviceID))
	}

	samples := make([]Sample, 0, len(active))
	for _, item := range active {
		sample := Sample{InstanceID: item.InstanceID, PID: item.PID, GPUDevices: []string{}, GPUs: []GPUUsage{}, CollectedAt: collectedAt}
		sample.CPUPercent, sample.MemoryUsedBytes = c.processUsage(item.PID)

		byDevice := map[string]*GPUUsage{}
		ensureGPU := func(deviceID string) *GPUUsage {
			if existing := byDevice[deviceID]; existing != nil {
				return existing
			}
			usage := &GPUUsage{DeviceID: deviceID}
			byDevice[deviceID] = usage
			return usage
		}

		for _, process := range snapshot.Processes {
			resolvedPID := resolveReportedPID(process.PID)
			if resolvedPID == item.PID {
				logPIDMapping(process.PID, resolvedPID, process.DeviceID)
			}
			if resolvedPID != item.PID || process.DeviceID == "" {
				continue
			}
			used := process.UsedBytes
			ensureGPU(process.DeviceID).VRAMUsedBytes = &used
		}
		for key, utilization := range nvidiaUtil {
			resolvedPID := resolveReportedPID(key.pid)
			if resolvedPID == item.PID {
				logPIDMapping(key.pid, resolvedPID, key.deviceID)
			}
			if resolvedPID != item.PID {
				continue
			}
			value := utilization
			ensureGPU(key.deviceID).UtilizationPct = &value
		}
		for _, process := range amdProcesses {
			resolvedPID := resolveReportedPID(process.pid)
			if resolvedPID == item.PID {
				logPIDMapping(process.pid, resolvedPID, process.deviceID)
			}
			if resolvedPID != item.PID {
				continue
			}
			usage := ensureGPU(process.deviceID)
			used := process.vramBytes
			usage.VRAMUsedBytes = &used
			if process.utilization != nil {
				value := *process.utilization
				usage.UtilizationPct = &value
			}
		}

		deviceIDs := make([]string, 0, len(byDevice))
		for deviceID := range byDevice {
			deviceIDs = append(deviceIDs, deviceID)
		}
		sort.Strings(deviceIDs)
		var vramTotal int64
		vramKnown := false
		var utilizationTotal float64
		utilizationCount := 0
		for _, deviceID := range deviceIDs {
			usage := byDevice[deviceID]
			sample.GPUDevices = append(sample.GPUDevices, deviceID)
			sample.GPUs = append(sample.GPUs, *usage)
			if usage.VRAMUsedBytes != nil {
				vramTotal += *usage.VRAMUsedBytes
				vramKnown = true
			}
			if usage.UtilizationPct != nil {
				utilizationTotal += *usage.UtilizationPct
				utilizationCount++
			}
		}
		if vramKnown {
			sample.VRAMUsedBytes = int64Ptr(vramTotal)
		}
		if utilizationCount > 0 {
			// Each process utilization is relative to one assigned device. Averaging
			// yields the Instance's utilization of its assigned GPU capacity and
			// never substitutes whole-device utilization for process utilization.
			sample.GPUUtilizationPct = float64Ptr(utilizationTotal / float64(utilizationCount))
		}
		samples = append(samples, sample)
	}

	for pid := range c.cpuPrev {
		if _, ok := activePIDs[pid]; !ok {
			delete(c.cpuPrev, pid)
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].InstanceID < samples[j].InstanceID })
	return samples
}

// runtimePIDResolver translates GPU-tool PIDs into the PID namespace used by
// the manager. NVIDIA/AMD tools commonly report the host PID even when the
// manager and llama-server run inside a container. When LCM_HOST_PROC points at
// a read-only host /proc mount, NSpid gives the corresponding container PID.
// Direct PID matches always win so native/non-container deployments are
// unaffected.
func (c *Collector) runtimePIDResolver(activePIDs map[int]struct{}) func(int) int {
	cache := map[int]int{}
	return func(reportedPID int) int {
		if reportedPID <= 0 {
			return reportedPID
		}
		if _, ok := activePIDs[reportedPID]; ok {
			return reportedPID
		}
		if mapped, ok := cache[reportedPID]; ok {
			return mapped
		}
		mapped := reportedPID
		if c.hostProcRoot != "" {
			root := strings.TrimRight(c.hostProcRoot, "/")
			status, err := c.readFile(root + "/" + strconv.Itoa(reportedPID) + "/status")
			if err == nil {
				if namespacePIDs, ok := parseNamespacePIDs(string(status)); ok {
					for index := len(namespacePIDs) - 1; index >= 0; index-- {
						candidate := namespacePIDs[index]
						if _, active := activePIDs[candidate]; active {
							mapped = candidate
							break
						}
					}
				}
			}
		}
		cache[reportedPID] = mapped
		return mapped
	}
}

type gpuProcessKey struct {
	pid      int
	deviceID string
}

func (c *Collector) nvidiaProcessUtilization(ctx context.Context) map[gpuProcessKey]float64 {
	attempts := [][]string{
		{"pmon", "-c", "1", "-s", "u"},
		{"pmon", "-c", "1"},
	}
	for index, args := range attempts {
		out, err := c.run(ctx, "nvidia-smi", args...)
		if err != nil {
			continue
		}
		if result := parseNVIDIAPMon(out); len(result) > 0 {
			return result
		}
		if index == 0 {
			systemlog.Log(systemlog.Debug, "telemetry", "nvidia-smi pmon -s u returned no process rows, retrying plain pmon")
		}
	}
	return nil
}

func parseNVIDIAPMon(out []byte) map[gpuProcessKey]float64 {
	result := map[gpuProcessKey]float64{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		gpuIndex, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil || pid <= 0 || fields[3] == "-" {
			continue
		}
		utilization, err := strconv.ParseFloat(strings.TrimSuffix(fields[3], "%"), 64)
		if err != nil {
			continue
		}
		result[gpuProcessKey{pid: pid, deviceID: "CUDA" + strconv.Itoa(gpuIndex)}] = clampPercent(utilization)
	}
	return result
}

func (c *Collector) amdProcesses(ctx context.Context, collectedAt time.Time) []amdProcess {
	out, err := c.run(ctx, "amd-smi", "process", "--csv")
	if err != nil {
		return nil
	}
	reader := csv.NewReader(strings.NewReader(string(out)))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil || len(records) < 2 {
		return nil
	}

	header := map[string]int{}
	for index, value := range records[0] {
		header[normalizeHeader(value)] = index
	}
	gpuIndex, gpuOK := header["gpu"]
	pidIndex, pidOK := header["pid"]
	vramIndex, vramOK := header["vram_mem"]
	gfxIndex, gfxOK := header["gfx"]
	if !gpuOK || !pidOK || !vramOK {
		return nil
	}

	next := map[string]amdPoint{}
	processes := make([]amdProcess, 0, len(records)-1)
	for _, record := range records[1:] {
		if gpuIndex >= len(record) || pidIndex >= len(record) || vramIndex >= len(record) {
			continue
		}
		gpu, err := strconv.Atoi(strings.TrimSpace(record[gpuIndex]))
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(record[pidIndex]))
		if err != nil || pid <= 0 {
			continue
		}
		vram, _ := parseByteValue(record[vramIndex])
		deviceID := "ROCm" + strconv.Itoa(gpu)
		key := strconv.Itoa(pid) + "@" + deviceID
		process := amdProcess{pid: pid, deviceID: deviceID, vramBytes: vram}
		if gfxOK && gfxIndex < len(record) {
			if gfx, ok := parseNanoseconds(record[gfxIndex]); ok {
				next[key] = amdPoint{gfxNS: gfx, at: collectedAt}
				if previous, ok := c.amdPrev[key]; ok && gfx > previous.gfxNS && collectedAt.After(previous.at) {
					elapsed := collectedAt.Sub(previous.at).Nanoseconds()
					if elapsed > 0 {
						utilization := clampPercent(float64(gfx-previous.gfxNS) / float64(elapsed) * 100)
						process.utilization = &utilization
					}
				}
			}
		}
		processes = append(processes, process)
	}
	c.amdPrev = next
	return processes
}

func (c *Collector) processUsage(pid int) (*float64, *int64) {
	var cpuPercent *float64
	processData, processErr := c.readFile("/proc/" + strconv.Itoa(pid) + "/stat")
	systemData, systemErr := c.readFile("/proc/stat")
	if processErr == nil && systemErr == nil {
		processTicks, processOK := parseProcessTicks(string(processData))
		systemTicks, systemOK := parseSystemTicks(string(systemData))
		if processOK && systemOK {
			if previous, ok := c.cpuPrev[pid]; ok && processTicks >= previous.processTicks && systemTicks > previous.systemTicks {
				cpu := float64(processTicks-previous.processTicks) / float64(systemTicks-previous.systemTicks) * float64(max(1, c.numCPU)) * 100
				cpuPercent = float64Ptr(cpu)
			}
			c.cpuPrev[pid] = cpuPoint{processTicks: processTicks, systemTicks: systemTicks}
		}
	}

	var memoryBytes *int64
	if status, err := c.readFile("/proc/" + strconv.Itoa(pid) + "/status"); err == nil {
		if rss, ok := parseRSSBytes(string(status)); ok {
			memoryBytes = int64Ptr(rss)
		}
	}
	return cpuPercent, memoryBytes
}

func parseProcessTicks(value string) (uint64, bool) {
	end := strings.LastIndex(value, ")")
	if end < 0 || end+1 >= len(value) {
		return 0, false
	}
	fields := strings.Fields(value[end+1:])
	if len(fields) <= 12 {
		return 0, false
	}
	user, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, false
	}
	system, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, false
	}
	return user + system, true
}

func parseSystemTicks(value string) (uint64, bool) {
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "cpu" {
			continue
		}
		limit := min(len(fields), 9) // exclude guest/guest_nice, already included in user/nice
		var total uint64
		for _, field := range fields[1:limit] {
			parsed, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return 0, false
			}
			total += parsed
		}
		return total, total > 0
	}
	return 0, false
}

func parseRSSBytes(value string) (int64, bool) {
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.TrimSuffix(fields[0], ":") != "VmRSS" {
			continue
		}
		kilobytes, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kilobytes * 1024, true
	}
	return 0, false
}

func parseNamespacePIDs(value string) ([]int, bool) {
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.TrimSuffix(fields[0], ":") != "NSpid" {
			continue
		}
		pids := make([]int, 0, len(fields)-1)
		for _, field := range fields[1:] {
			pid, err := strconv.Atoi(field)
			if err != nil || pid <= 0 {
				return nil, false
			}
			pids = append(pids, pid)
		}
		return pids, len(pids) > 0
	}
	return nil, false
}

func parseNanoseconds(value string) (uint64, bool) {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return 0, false
	}
	amount, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || amount < 0 {
		return 0, false
	}
	multiplier := float64(1)
	if len(fields) > 1 {
		switch strings.ToLower(strings.TrimSpace(fields[1])) {
		case "ns":
		case "us", "µs":
			multiplier = 1_000
		case "ms":
			multiplier = 1_000_000
		case "s":
			multiplier = 1_000_000_000
		default:
			return 0, false
		}
	}
	return uint64(amount * multiplier), true
}

func parseByteValue(value string) (int64, bool) {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) == 0 {
		return 0, false
	}
	amount, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	multiplier := float64(1)
	if len(fields) > 1 {
		switch strings.ToUpper(strings.TrimSpace(fields[1])) {
		case "KB", "KIB":
			multiplier = 1024
		case "MB", "MIB":
			multiplier = 1024 * 1024
		case "GB", "GIB":
			multiplier = 1024 * 1024 * 1024
		case "TB", "TIB":
			multiplier = 1024 * 1024 * 1024 * 1024
		case "B", "BYTES":
		default:
			return 0, false
		}
	}
	return int64(amount * multiplier), true
}

func normalizeHeader(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func int64Ptr(value int64) *int64       { return &value }
func float64Ptr(value float64) *float64 { return &value }
