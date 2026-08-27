package hardware

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func commandNotFound(name string) error {
	return &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func TestSnapshotParsesNVIDIAAndRAM(t *testing.T) {
	d := New()
	d.now = func() time.Time { return time.Unix(100, 0) }
	d.readFile = func(path string) ([]byte, error) {
		return []byte("MemTotal:       65536 kB\nMemAvailable:   32768 kB\n"), nil
	}
	d.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name != "nvidia-smi" {
			return nil, commandNotFound(name)
		}
		if strings.Contains(joined, "query-gpu") {
			return []byte("0, GPU-aaa, RTX 4090, 24564, 1024, 12\n1, GPU-bbb, RTX 3090, 24576, 2048, 33\n"), nil
		}
		if strings.Contains(joined, "query-compute-apps") {
			return []byte("1234, GPU-aaa, 512, llama-server\n"), nil
		}
		return nil, errors.New("unexpected command")
	}
	snapshot, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RAMTotalBytes != 65536*1024 || snapshot.RAMAvailableBytes != 32768*1024 {
		t.Fatalf("unexpected RAM snapshot: %+v", snapshot)
	}
	if len(snapshot.GPUs) != 2 || snapshot.GPUs[0].ID != "CUDA0" || snapshot.GPUs[1].ID != "CUDA1" {
		t.Fatalf("unexpected NVIDIA GPUs: %+v", snapshot.GPUs)
	}
	if len(snapshot.Processes) != 1 || snapshot.Processes[0].DeviceID != "CUDA0" || snapshot.Processes[0].PID != 1234 {
		t.Fatalf("unexpected NVIDIA process list: %+v", snapshot.Processes)
	}
}

func TestSnapshotParsesROCm(t *testing.T) {
	d := New()
	d.readFile = func(string) ([]byte, error) { return nil, errors.New("missing") }
	d.run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		switch name {
		case "nvidia-smi":
			return nil, commandNotFound(name)
		case "rocm-smi":
			return []byte(`{"card0":{"Card series":"AMD Radeon RX 7900 XTX","Unique ID":"abc","GPU use (%)":"7","VRAM Total Memory (B)":"25753026560","VRAM Total Used Memory (B)":"172716032"}}`), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}
	snapshot, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.GPUs) != 1 {
		t.Fatalf("expected one AMD GPU, got %+v", snapshot.GPUs)
	}
	gpu := snapshot.GPUs[0]
	if gpu.ID != "ROCm0" || gpu.Backend != "rocm" || gpu.TotalBytes != 25753026560 || gpu.UsedBytes != 172716032 {
		t.Fatalf("unexpected AMD GPU: %+v", gpu)
	}
}

func TestSnapshotWithoutGPUUtilitiesStillReturnsRAM(t *testing.T) {
	d := New()
	d.readFile = func(string) ([]byte, error) { return []byte("MemTotal: 1000 kB\nMemAvailable: 500 kB\n"), nil }
	d.run = func(_ context.Context, name string, _ ...string) ([]byte, error) { return nil, commandNotFound(name) }
	snapshot, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.GPUs) != 0 || snapshot.RAMAvailableBytes != 500*1024 {
		t.Fatalf("unexpected CPU-only snapshot: %+v", snapshot)
	}
}

func TestSnapshotReturnsProbeErrorsWhenNoGPUCanBeRead(t *testing.T) {
	d := New()
	d.readFile = func(string) ([]byte, error) { return nil, errors.New("meminfo missing") }
	d.run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		return nil, errors.New(name + " failed")
	}
	snapshot, err := d.Snapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "nvidia-smi failed") || !strings.Contains(err.Error(), "rocm-smi failed") {
		t.Fatalf("expected joined probe errors, snapshot=%+v err=%v", snapshot, err)
	}
}

func TestGPUParsersSkipMalformedRowsAndUseFallbacks(t *testing.T) {
	d := New()
	d.run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		switch name {
		case "nvidia-smi":
			return []byte("\ninvalid\nx, GPU-x, Invalid index, 10, 1, 3\n0, GPU-zero, Tiny GPU, 1, 2, 4\n"), nil
		case "rocm-smi":
			return []byte(`{
				"card7":{"Card model":" Radeon Test ","Unique ID (Hex)":" id-7 ","GPU use (%)":"12%","VRAM Total Memory (B)":"2000","VRAM Total Used Memory (B)":"500"},
				"cardX":{"GPU use (%)":7.5,"VRAM Total Memory (B)":1000,"VRAM Total Used Memory (B)":250}
			}`), nil
		default:
			return nil, errors.New("unexpected command")
		}
	}

	nvidia, err := d.nvidiaGPUs(context.Background())
	if err != nil || len(nvidia) != 1 || nvidia[0].ID != "CUDA0" || nvidia[0].FreeBytes != 0 {
		t.Fatalf("unexpected NVIDIA parse: %+v err=%v", nvidia, err)
	}
	rocm, err := d.rocmGPUs(context.Background())
	if err != nil || len(rocm) != 2 {
		t.Fatalf("unexpected ROCm parse: %+v err=%v", rocm, err)
	}
	if rocm[0].ID != "ROCm7" || rocm[0].Name != "Radeon Test" || rocm[0].UUID != "id-7" || rocm[0].UtilizationPct != 12 {
		t.Fatalf("unexpected named ROCm GPU: %+v", rocm[0])
	}
	if rocm[1].ID != "ROCm1" || rocm[1].Name != "AMD GPU 1" || rocm[1].UtilizationPct != 7.5 {
		t.Fatalf("unexpected fallback ROCm GPU: %+v", rocm[1])
	}

	d.run = func(context.Context, string, ...string) ([]byte, error) { return []byte("not json"), nil }
	if _, err := d.rocmGPUs(context.Background()); err == nil {
		t.Fatal("expected malformed ROCm JSON to fail")
	}
}

func TestNVIDIAProcessParserAndHelperFallbacks(t *testing.T) {
	d := New()
	if processes, err := d.nvidiaProcesses(context.Background(), []GPU{{ID: "ROCm0", Backend: "rocm", UUID: "amd"}}); err != nil || processes != nil {
		t.Fatalf("non-CUDA process probe should be skipped: %+v err=%v", processes, err)
	}
	d.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("bad\nx, GPU-a, 10\n1, GPU-missing, 10\n2, GPU-a, 10\n3, GPU-a, 20, worker\n"), nil
	}
	processes, err := d.nvidiaProcesses(context.Background(), []GPU{{ID: "CUDA0", Backend: "cuda", UUID: "GPU-a"}})
	if err != nil || len(processes) != 2 {
		t.Fatalf("unexpected processes: %+v err=%v", processes, err)
	}
	if processes[0].PID != 2 || processes[0].ProcessName != "" || processes[1].ProcessName != "worker" {
		t.Fatalf("unexpected parsed process metadata: %+v", processes)
	}

	if got := trailingIndex("card999999999999999999999999", 6); got != 6 {
		t.Fatalf("overflowing card index should use fallback, got %d", got)
	}
	if got := firstValue(map[string]any{" Other ": "x"}, "missing"); got != nil {
		t.Fatalf("missing first value=%v", got)
	}
	if got := findValue(map[string]any{" GPU use (%) ": "9"}, "gpu use (%)"); got != "9" {
		t.Fatalf("case-insensitive value lookup=%v", got)
	}
	if int64Value(nil) != 0 || float64Value(nil) != 0 || stringValue(42) != "" || stringValue(nil) != "" {
		t.Fatal("unexpected scalar fallback conversion")
	}
}
