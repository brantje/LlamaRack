package hardware

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNVIDIAMemoryBandwidthTelemetry(t *testing.T) {
	d := New()
	d.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "nvidia-smi" {
			return nil, errors.New("unexpected command")
		}
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "pcie.link.gen.max"):
			return []byte("0, 4, 8\n1, 4, 16\n"), nil
		case strings.Contains(joined, "--query-gpu="):
			return []byte("0, GPU-a, NVIDIA GeForce RTX 4060 Ti, 16380, 1000, 4, 9001\n1, GPU-b, NVIDIA GeForce RTX 4090, 24564, 2000, 7, 10501\n"), nil
		case joined == "-q":
			return []byte("GPU 00000000:01:00.0\n    Memory Bus Width                  : 128 bits\nGPU 00000000:02:00.0\n    Memory Bus Width                  : 384 bits\n"), nil
		default:
			return nil, errors.New("unexpected nvidia-smi arguments")
		}
	}

	gpus, err := d.nvidiaGPUs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 2 {
		t.Fatalf("gpus=%+v", gpus)
	}
	if got, want := gpus[0].MemoryBandwidthBytesPerSecond, int64(288_032_000_000); got != want {
		t.Fatalf("4060 Ti bandwidth=%d want=%d", got, want)
	}
	if got, want := gpus[1].MemoryBandwidthBytesPerSecond, int64(1_008_096_000_000); got != want {
		t.Fatalf("4090 bandwidth=%d want=%d", got, want)
	}
	if got, want := gpus[0].PCIeBandwidthBytesPerSecond, int64(15_753_846_153); got != want {
		t.Fatalf("4060 Ti PCIe bandwidth=%d want=%d", got, want)
	}
	if got, want := gpus[1].PCIeBandwidthBytesPerSecond, int64(31_507_692_307); got != want {
		t.Fatalf("4090 PCIe bandwidth=%d want=%d", got, want)
	}
}

func TestNVIDIABandwidthProbeIsBestEffort(t *testing.T) {
	d := New()
	calls := 0
	d.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "clocks.max.memory") {
			return nil, errors.New("field unsupported")
		}
		if strings.Contains(joined, "pcie.link.gen.max") {
			return nil, errors.New("pcie unsupported")
		}
		if strings.Contains(joined, "--query-gpu=") {
			return []byte("0, GPU-a, Legacy GPU, 1024, 0, 0\n"), nil
		}
		if joined == "-q" {
			return nil, errors.New("query unsupported")
		}
		return nil, errors.New("unexpected")
	}
	gpus, err := d.nvidiaGPUs(context.Background())
	if err != nil || len(gpus) != 1 {
		t.Fatalf("gpus=%+v err=%v", gpus, err)
	}
	if gpus[0].MemoryBandwidthBytesPerSecond != 0 || gpus[0].PCIeBandwidthBytesPerSecond != 0 {
		t.Fatalf("bandwidth should remain unknown: %+v", gpus[0])
	}
	if calls < 4 {
		t.Fatalf("expected enriched query, fallback query, best-effort -q and PCIe probe, calls=%d", calls)
	}
}

func TestROCmPCIeBandwidthTelemetry(t *testing.T) {
	d := New()
	d.readFile = func(path string) ([]byte, error) {
		switch {
		case strings.HasSuffix(path, "max_link_speed"):
			return []byte("16.0 GT/s PCIe\n"), nil
		case strings.HasSuffix(path, "max_link_width"):
			return []byte("16\n"), nil
		default:
			return nil, errors.New("missing")
		}
	}
	if got, want := d.rocmPCIeBandwidth(3), int64(31_507_692_307); got != want {
		t.Fatalf("ROCm PCIe bandwidth=%d want=%d", got, want)
	}
}

func TestMemoryBandwidthHelpers(t *testing.T) {
	widths := parseNVIDIAMemoryBusWidths("Memory Bus Width: N/A\nMemory Bus Width: 256 bits\nnot a width\n")
	if len(widths) != 1 || widths[0] != 256 {
		t.Fatalf("widths=%v", widths)
	}
	if theoreticalMemoryBandwidth(0, 256) != 0 || theoreticalMemoryBandwidth(1000, 0) != 0 {
		t.Fatal("invalid telemetry should not produce bandwidth")
	}
	if got := theoreticalMemoryBandwidth(1000, 256); got != 64_000_000_000 {
		t.Fatalf("bandwidth=%d", got)
	}
	if theoreticalPCIeBandwidth(0, 16) != 0 || theoreticalPCIeBandwidth(4, 0) != 0 || theoreticalPCIeBandwidth(99, 16) != 0 {
		t.Fatal("invalid PCIe telemetry should not produce bandwidth")
	}
	if got, want := theoreticalPCIeBandwidth(4, 8), int64(15_753_846_153); got != want {
		t.Fatalf("PCIe 4 x8=%d want=%d", got, want)
	}
	if got, want := pcieBandwidthForTransfers(5, 16), int64(8_000_000_000); got != want {
		t.Fatalf("PCIe 2 x16=%d want=%d", got, want)
	}
	if parsePCIeTransfersPerSecond("16.0 GT/s PCIe") != 16 || parsePCIeTransfersPerSecond("bad") != 0 {
		t.Fatal("unexpected PCIe speed parse")
	}
}

func TestHostMemoryBandwidthBenchmarkProducesTelemetry(t *testing.T) {
	if got := benchmarkHostMemoryBandwidth(); got <= 0 {
		t.Fatalf("host memory bandwidth=%d", got)
	}
}
