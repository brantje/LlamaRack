package telemetry

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func TestCollectNVIDIATelemetryIsAttributedByInstancePID(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	collector := New(func(context.Context) (hardware.Snapshot, error) {
		return hardware.Snapshot{Processes: []hardware.GPUProcess{
			{PID: 42, DeviceID: "CUDA0", UsedBytes: 3 * gib},
			{PID: 42, DeviceID: "CUDA1", UsedBytes: 5 * gib},
			{PID: 99, DeviceID: "CUDA0", UsedBytes: 7 * gib},
		}}, nil
	})
	collector.numCPU = 4
	collector.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "nvidia-smi" {
			return []byte("# gpu pid type sm mem enc dec command\n0 42 C 20 1 - - llama-server\n1 42 C 40 2 - - llama-server\n0 99 C 90 3 - - other\n"), nil
		}
		return nil, errors.New("not installed")
	}
	processTicks := uint64(150)
	systemTicks := uint64(1000)
	collector.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/proc/42/stat":
			return []byte(processStat(42, processTicks-50, 50)), nil
		case "/proc/stat":
			return []byte(systemStat(systemTicks)), nil
		case "/proc/42/status":
			return []byte("Name:\tllama-server\nVmRSS:\t2048 kB\n"), nil
		default:
			return nil, errors.New("missing")
		}
	}

	runtimes := []supervisor.Runtime{
		{InstanceID: "coder", ModelID: "m1", State: supervisor.Ready, PID: 42},
		{InstanceID: "stopped", ModelID: "m2", State: supervisor.Unloaded},
	}
	first := collector.Collect(context.Background(), runtimes)
	if len(first) != 1 {
		t.Fatalf("samples=%+v", first)
	}
	got := first[0]
	if got.InstanceID != "coder" || got.PID != 42 {
		t.Fatalf("identity=%+v", got)
	}
	if strings.Join(got.GPUDevices, ",") != "CUDA0,CUDA1" || len(got.GPUs) != 2 {
		t.Fatalf("placement=%+v", got)
	}
	if got.VRAMUsedBytes == nil || *got.VRAMUsedBytes != 8*gib {
		t.Fatalf("vram=%v", got.VRAMUsedBytes)
	}
	if got.GPUUtilizationPct == nil || *got.GPUUtilizationPct != 30 {
		t.Fatalf("instance GPU utilization=%v", got.GPUUtilizationPct)
	}
	if got.CPUPercent != nil {
		t.Fatalf("first CPU sample should establish a baseline, got %v", *got.CPUPercent)
	}
	if got.MemoryUsedBytes == nil || *got.MemoryUsedBytes != 2048*1024 {
		t.Fatalf("rss=%v", got.MemoryUsedBytes)
	}

	processTicks = 250
	systemTicks = 1200
	second := collector.Collect(context.Background(), runtimes)
	if len(second) != 1 || second[0].CPUPercent == nil || math.Abs(*second[0].CPUPercent-200) > 0.001 {
		t.Fatalf("second CPU sample=%+v", second)
	}
}

func TestCollectMapsGPUHostPIDsIntoManagerNamespace(t *testing.T) {
	const mib = int64(1024 * 1024)
	collector := New(func(context.Context) (hardware.Snapshot, error) {
		return hardware.Snapshot{Processes: []hardware.GPUProcess{
			{PID: 2554129, DeviceID: "CUDA0", UsedBytes: 14260 * mib},
			{PID: 2555000, DeviceID: "CUDA0", UsedBytes: 600 * mib},
		}}, nil
	})
	collector.hostProcRoot = "/host/proc"
	collector.run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "nvidia-smi" {
			return []byte("# gpu pid type sm mem enc dec command\n0 2554129 C 97 1 - - llama-server\n0 2555000 C 3 1 - - llama-server\n"), nil
		}
		return nil, errors.New("not installed")
	}
	collector.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/host/proc/2554129/status":
			return []byte("Name:\tllama-server\nNSpid:\t2554129\t1652\n"), nil
		case "/host/proc/2555000/status":
			return []byte("Name:\tllama-server\nNSpid:\t2555000\t1777\n"), nil
		default:
			return nil, errors.New("missing")
		}
	}

	samples := collector.Collect(context.Background(), []supervisor.Runtime{
		{InstanceID: "gemma-4", ModelID: "m1", State: supervisor.Ready, PID: 1652},
		{InstanceID: "other", ModelID: "m2", State: supervisor.Ready, PID: 1777},
	})
	if len(samples) != 2 {
		t.Fatalf("samples=%+v", samples)
	}
	if samples[0].InstanceID != "gemma-4" || samples[0].PID != 1652 {
		t.Fatalf("gemma identity=%+v", samples[0])
	}
	if strings.Join(samples[0].GPUDevices, ",") != "CUDA0" || samples[0].VRAMUsedBytes == nil || *samples[0].VRAMUsedBytes != 14260*mib {
		t.Fatalf("gemma placement/vram=%+v", samples[0])
	}
	if samples[0].GPUUtilizationPct == nil || *samples[0].GPUUtilizationPct != 97 {
		t.Fatalf("gemma process GPU utilization=%v", samples[0].GPUUtilizationPct)
	}
	if samples[1].InstanceID != "other" || samples[1].GPUUtilizationPct == nil || *samples[1].GPUUtilizationPct != 3 {
		t.Fatalf("other attribution=%+v", samples[1])
	}
}

func TestRuntimePIDResolverPrefersDirectActivePID(t *testing.T) {
	collector := New(nil)
	collector.hostProcRoot = "/host/proc"
	collector.readFile = func(path string) ([]byte, error) {
		if path == "/host/proc/42/status" {
			return []byte("NSpid:\t42\t7\n"), nil
		}
		return nil, errors.New("missing")
	}
	resolve := collector.runtimePIDResolver(map[int]struct{}{42: {}, 7: {}})
	if got := resolve(42); got != 42 {
		t.Fatalf("direct active PID must win, got %d", got)
	}
}

func TestCollectAMDUsesPerProcessMemoryAndEngineTimeWhenAvailable(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	amdCall := 0
	collector := New(func(context.Context) (hardware.Snapshot, error) { return hardware.Snapshot{}, nil })
	collector.now = func() time.Time { return now }
	collector.run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		switch name {
		case "nvidia-smi":
			return nil, errors.New("no NVIDIA")
		case "amd-smi":
			amdCall++
			gfx := "1000000000 ns"
			if amdCall > 1 {
				gfx = "1500000000 ns"
			}
			return []byte("gpu,name,pid,gtt_mem,cpu_mem,vram_mem,mem_usage,gfx,enc\n0,llama-server,77,0 B,0 B,4.0 GB,4.0 GB," + gfx + ",0\n1,other,88,0 B,0 B,8.0 GB,8.0 GB,1900000000,0\n"), nil
		default:
			return nil, errors.New("unexpected")
		}
	}
	collector.readFile = func(string) ([]byte, error) { return nil, errors.New("proc unavailable") }
	runtimes := []supervisor.Runtime{{InstanceID: "amd", ModelID: "m", State: supervisor.Ready, PID: 77}}

	first := collector.Collect(context.Background(), runtimes)
	if len(first) != 1 || len(first[0].GPUs) != 1 || first[0].GPUs[0].DeviceID != "ROCm0" {
		t.Fatalf("first=%+v", first)
	}
	if first[0].VRAMUsedBytes == nil || *first[0].VRAMUsedBytes != 4*1024*1024*1024 {
		t.Fatalf("AMD VRAM=%v", first[0].VRAMUsedBytes)
	}
	if first[0].GPUUtilizationPct != nil {
		t.Fatalf("first cumulative AMD engine sample is not utilization: %v", first[0].GPUUtilizationPct)
	}

	now = now.Add(time.Second)
	second := collector.Collect(context.Background(), runtimes)
	if second[0].GPUUtilizationPct == nil || math.Abs(*second[0].GPUUtilizationPct-50) > 0.001 {
		t.Fatalf("AMD process utilization=%v", second[0].GPUUtilizationPct)
	}
}

func TestCollectDoesNotSubstituteWholeGPUUtilization(t *testing.T) {
	collector := New(func(context.Context) (hardware.Snapshot, error) {
		return hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", UtilizationPct: 97}}, Processes: []hardware.GPUProcess{{PID: 7, DeviceID: "CUDA0", UsedBytes: 123}}}, nil
	})
	collector.run = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("process utilization unavailable")
	}
	collector.readFile = func(string) ([]byte, error) { return nil, errors.New("missing") }
	samples := collector.Collect(context.Background(), []supervisor.Runtime{{InstanceID: "one", PID: 7, State: supervisor.Ready}})
	if len(samples) != 1 || samples[0].GPUUtilizationPct != nil || samples[0].GPUs[0].UtilizationPct != nil {
		t.Fatalf("whole-GPU utilization leaked into Instance telemetry: %+v", samples)
	}
}

func TestCollectorParsersAndFailureFallbacks(t *testing.T) {
	collector := New(nil)
	collector.run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "nvidia-smi" {
			return []byte("# header\nbad\nx 1 C 3\n0 nope C 3\n0 2 C -\n0 3 C nope\n0 4 C 120\n1 5 C -2%\n"), nil
		}
		return []byte("gpu,pid\n0,1\n"), nil
	}
	util := collector.nvidiaProcessUtilization(context.Background())
	if util[gpuProcessKey{pid: 4, deviceID: "CUDA0"}] != 100 || util[gpuProcessKey{pid: 5, deviceID: "CUDA1"}] != 0 {
		t.Fatalf("clamped NVIDIA process utilization=%+v", util)
	}
	if got := collector.amdProcesses(context.Background(), time.Now()); got != nil {
		t.Fatalf("missing AMD columns should be ignored: %+v", got)
	}

	if ticks, ok := parseProcessTicks(processStat(1, 11, 7)); !ok || ticks != 18 {
		t.Fatalf("process ticks=%d ok=%v", ticks, ok)
	}
	for _, invalid := range []string{"no closing paren", "1 (x) R 1", "1 (x) R 1 1 1 1 1 1 1 1 1 1 nope 2"} {
		if _, ok := parseProcessTicks(invalid); ok {
			t.Fatalf("invalid process stat accepted: %q", invalid)
		}
	}
	if ticks, ok := parseSystemTicks("intr 1\ncpu 10 20 30 40 5 6 7 8 999 999\n"); !ok || ticks != 126 {
		t.Fatalf("system ticks=%d ok=%v", ticks, ok)
	}
	if _, ok := parseSystemTicks("cpu 1 nope"); ok {
		t.Fatal("invalid system stat accepted")
	}
	if rss, ok := parseRSSBytes("VmSize: 1 kB\nVmRSS: 12 kB\n"); !ok || rss != 12*1024 {
		t.Fatalf("rss=%d ok=%v", rss, ok)
	}
	if _, ok := parseRSSBytes("VmRSS: nope kB"); ok {
		t.Fatal("invalid RSS accepted")
	}
	if pids, ok := parseNamespacePIDs("Name:\tx\nNSpid:\t2554129\t1652\n"); !ok || len(pids) != 2 || pids[0] != 2554129 || pids[1] != 1652 {
		t.Fatalf("namespace pids=%v ok=%v", pids, ok)
	}
	for _, invalid := range []string{"Name: x", "NSpid:", "NSpid: 10 nope", "NSpid: 10 0"} {
		if _, ok := parseNamespacePIDs(invalid); ok {
			t.Fatalf("invalid NSpid accepted: %q", invalid)
		}
	}

	cases := map[string]int64{
		"42":     42,
		"2 KiB":  2 * 1024,
		"3 MB":   3 * 1024 * 1024,
		"1.5 GB": int64(1.5 * 1024 * 1024 * 1024),
		"1 TB":   1024 * 1024 * 1024 * 1024,
		"5 B":    5,
	}
	for input, want := range cases {
		if got, ok := parseByteValue(input); !ok || got != want {
			t.Fatalf("parseByteValue(%q)=%d,%v want=%d", input, got, ok, want)
		}
	}
	for _, invalid := range []string{"", "wat GB", "1 PB"} {
		if _, ok := parseByteValue(invalid); ok {
			t.Fatalf("invalid byte value accepted: %q", invalid)
		}
	}
	if got, ok := parseNanoseconds("1.5 ms"); !ok || got != 1_500_000 {
		t.Fatalf("duration=%d ok=%v", got, ok)
	}
	for _, invalid := range []string{"", "wat ns", "-1 ns", "1 minutes"} {
		if _, ok := parseNanoseconds(invalid); ok {
			t.Fatalf("invalid duration accepted: %q", invalid)
		}
	}
	if normalizeHeader(" VRAM-MEM ") != "vram_mem" || clampPercent(-1) != 0 || clampPercent(150) != 100 || clampPercent(42) != 42 {
		t.Fatal("helper normalization/clamping failed")
	}

	collector.cpuPrev[1] = cpuPoint{processTicks: 1, systemTicks: 1}
	collector.amdPrev["1@ROCm0"] = amdPoint{gfxNS: 1, at: time.Now()}
	if samples := collector.Collect(context.Background(), []supervisor.Runtime{{InstanceID: "off", PID: 0}}); samples != nil || len(collector.cpuPrev) != 0 || len(collector.amdPrev) != 0 {
		t.Fatalf("inactive collection should reset baselines: samples=%+v cpu=%+v amd=%+v", samples, collector.cpuPrev, collector.amdPrev)
	}
}

func TestAMDParserSkipsMalformedRowsAndUnavailableCounters(t *testing.T) {
	collector := New(func(context.Context) (hardware.Snapshot, error) {
		return hardware.Snapshot{}, errors.New("hardware degraded")
	})
	now := time.Unix(10, 0)
	collector.amdPrev["9@ROCm0"] = amdPoint{gfxNS: 100, at: now.Add(-time.Second)}
	collector.run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "nvidia-smi" {
			return nil, errors.New("missing")
		}
		return []byte("gpu,pid,vram_mem,gfx\nX,9,1 GB,200\n0,nope,1 GB,200\n0,9,bad,100\n0,10,2 GB,bad\n0,11,3 GB,200\n"), nil
	}
	collector.readFile = func(string) ([]byte, error) { return nil, errors.New("missing") }
	processes := collector.amdProcesses(context.Background(), now)
	if len(processes) != 3 {
		t.Fatalf("parsed AMD rows=%+v", processes)
	}
	// A non-increasing/unchanged counter is deliberately unavailable rather than
	// being reported as 0%, because some AMD stacks expose a permanent zero.
	if processes[0].pid != 9 || processes[0].utilization != nil || processes[0].vramBytes != 0 {
		t.Fatalf("unavailable AMD utilization=%+v", processes[0])
	}

	collector.run = func(context.Context, string, ...string) ([]byte, error) { return []byte("\"unterminated"), nil }
	if got := collector.amdProcesses(context.Background(), now); got != nil {
		t.Fatalf("malformed CSV=%+v", got)
	}
}

func processStat(pid int, user, system uint64) string {
	return strconvI(pid) + " (llama server) R 1 1 1 0 0 0 0 0 0 0 " + strconvU(user) + " " + strconvU(system) + " 0 0"
}

func systemStat(total uint64) string {
	// Keep all activity in user to make expected deltas obvious.
	return "cpu " + strconvU(total) + " 0 0 0 0 0 0 0 0 0\n"
}

func strconvI(value int) string    { return strconv.Itoa(value) }
func strconvU(value uint64) string { return strconv.FormatUint(value, 10) }
