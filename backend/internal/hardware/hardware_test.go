package hardware

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSnapshotParsesNVIDIAAndRAM(t *testing.T) {
	d := New()
	d.now = func() time.Time { return time.Unix(100, 0) }
	d.readFile = func(path string) ([]byte, error) {
		return []byte("MemTotal:       65536 kB\nMemAvailable:   32768 kB\n"), nil
	}
	d.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name != "nvidia-smi" {
			return nil, errors.New("executable file not found")
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
			return nil, errors.New("executable file not found")
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
	d.run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("executable file not found") }
	snapshot, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.GPUs) != 0 || snapshot.RAMAvailableBytes != 500*1024 {
		t.Fatalf("unexpected CPU-only snapshot: %+v", snapshot)
	}
}
