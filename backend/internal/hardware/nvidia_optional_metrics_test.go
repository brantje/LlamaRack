package hardware

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNVIDIAMemoryBandwidthUsesDirectNVMLFieldWhenQOmitsBusWidth(t *testing.T) {
	d := New()
	d.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "nvidia-smi" {
			return nil, errors.New("unexpected command")
		}
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "--query-gpu=index,memory.bus_width"):
			return []byte("0, 128\n"), nil
		case strings.Contains(joined, "--query-gpu=index,clocks.max.memory"):
			return []byte("0, 9001\n"), nil
		case strings.Contains(joined, "--query-gpu=index,pcie.link.gen.max"):
			return nil, errors.New("pcie query hidden in container")
		case strings.Contains(joined, "--query-gpu=index,pci.bus_id"):
			return nil, errors.New("pci bus id unavailable")
		case strings.Contains(joined, "--query-gpu=index,uuid,name"):
			return []byte("0, GPU-a, NVIDIA GeForce RTX 4060 Ti, 16380, 1000, 4, 9001\n"), nil
		case joined == "-q":
			// This is the real-world failure mode: the ordinary inventory works but
			// consumer/container nvidia-smi omits Memory Bus Width from -q.
			return []byte("GPU 00000000:01:00.0\n    Product Name : NVIDIA GeForce RTX 4060 Ti\n"), nil
		default:
			return nil, errors.New("unexpected nvidia-smi arguments: " + joined)
		}
	}

	gpus, err := d.nvidiaGPUs(context.Background())
	if err != nil || len(gpus) != 1 {
		t.Fatalf("gpus=%+v err=%v", gpus, err)
	}
	if got, want := gpus[0].MemoryBandwidthBytesPerSecond, int64(288_032_000_000); got != want {
		t.Fatalf("direct NVML bandwidth=%d want=%d gpu=%+v", got, want, gpus[0])
	}
}

func TestNVIDIAPCIeFallsBackToLinuxSysfs(t *testing.T) {
	d := New()
	d.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "pcie.link.gen.max"):
			return nil, errors.New("NVML PCIe fields hidden")
		case strings.Contains(joined, "pci.bus_id"):
			return []byte("0, 00000000:01:00.0\n"), nil
		default:
			return nil, errors.New("unexpected query")
		}
	}
	d.readFile = func(path string) ([]byte, error) {
		switch {
		case strings.HasSuffix(path, "/0000:01:00.0/max_link_speed"):
			return []byte("16.0 GT/s PCIe\n"), nil
		case strings.HasSuffix(path, "/0000:01:00.0/max_link_width"):
			return []byte("8\n"), nil
		default:
			return nil, errors.New("missing sysfs path: " + path)
		}
	}

	gpus := []GPU{{ID: "CUDA0", Backend: "cuda", Index: 0, MemoryBandwidthBytesPerSecond: 288_032_000_000}}
	d.enrichNVIDIAPCIe(context.Background(), gpus)
	if got, want := gpus[0].PCIeBandwidthBytesPerSecond, int64(15_753_846_153); got != want {
		t.Fatalf("sysfs PCIe bandwidth=%d want=%d gpu=%+v", got, want, gpus[0])
	}
}

func TestNVIDIAOptionalMetricHelpers(t *testing.T) {
	if got := normalizePCIBusID("00000000:01:00.0"); got != "0000:01:00.0" {
		t.Fatalf("normalized bus id=%q", got)
	}
	if got := normalizePCIBusID("bad"); got != "" {
		t.Fatalf("invalid bus id=%q", got)
	}

	d := New()
	d.run = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("0, N/A\n1, 256 bits\ninvalid\n"), nil
	}
	values := d.nvidiaFloatMetric(context.Background(), "memory.bus_width")
	if len(values) != 1 || values[1] != 256 {
		t.Fatalf("metric values=%v", values)
	}
}
