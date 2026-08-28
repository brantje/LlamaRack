package hardware

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNVIDIAMemoryBandwidthFallsBackToKnown4060TiBusWidth(t *testing.T) {
	d := New()
	d.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "nvidia-smi" {
			return nil, errors.New("unexpected command")
		}
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "--query-gpu=index,uuid,name"):
			return []byte("0, GPU-a, NVIDIA GeForce RTX 4060 Ti, 16380, 2, 0, 9001\n"), nil
		case joined == "-q":
			return []byte("GPU 00000000:00:10.0\n    Product Name : NVIDIA GeForce RTX 4060 Ti\n    Minor Number : 0\n"), nil
		case strings.Contains(joined, "--query-gpu=index,memory.bus_width"):
			return nil, errors.New(`Field "memory.bus_width" is not a valid field to query.`)
		case strings.Contains(joined, "--query-gpu=index,clocks.max.memory"):
			return []byte("0, 9001\n"), nil
		case strings.Contains(joined, "--query-gpu=index,pcie.link.gen.max"):
			return []byte("0, 4, 8\n"), nil
		case strings.Contains(joined, "--query-gpu=index,pci.bus_id"):
			return []byte("0, 00000000:00:10.0\n"), nil
		default:
			return nil, errors.New("unexpected nvidia-smi arguments: " + joined)
		}
	}

	gpus, err := d.nvidiaGPUs(context.Background())
	if err != nil || len(gpus) != 1 {
		t.Fatalf("gpus=%+v err=%v", gpus, err)
	}
	if got, want := gpus[0].MemoryBandwidthBytesPerSecond, int64(288_032_000_000); got != want {
		t.Fatalf("fallback bandwidth=%d want=%d gpu=%+v", got, want, gpus[0])
	}
}

func TestNVIDIAPCIeFallsBackToHumanReadableQueryBeforeSysfs(t *testing.T) {
	d := New()
	d.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "memory.bus_width"):
			return []byte("0, 128\n"), nil
		case strings.Contains(joined, "clocks.max.memory"):
			return []byte("0, 9001\n"), nil
		case strings.Contains(joined, "pcie.link.gen.max"):
			return nil, errors.New("selective PCIe query unsupported")
		case joined == "-q":
			return []byte(`GPU 00000000:00:10.0
    Product Name : NVIDIA GeForce RTX 4060 Ti
    Minor Number : 0
    PCI
        GPU Link Info
            PCIe Generation
                Max : 4
                Current : 1
            Link Width
                Max : 8x
                Current : 8x
`), nil
		case strings.Contains(joined, "pci.bus_id"):
			return nil, errors.New("sysfs fallback should not be needed")
		default:
			return nil, errors.New("unexpected query: " + joined)
		}
	}
	d.readFile = func(path string) ([]byte, error) {
		return nil, errors.New("sysfs should not be read: " + path)
	}

	gpus := []GPU{{ID: "CUDA0", Backend: "cuda", Index: 0, Name: "NVIDIA GeForce RTX 4060 Ti"}}
	d.enrichNVIDIAPCIe(context.Background(), gpus)
	if got, want := gpus[0].PCIeBandwidthBytesPerSecond, int64(15_753_846_153); got != want {
		t.Fatalf("-q PCIe bandwidth=%d want=%d gpu=%+v", got, want, gpus[0])
	}
}

func TestKnownNVIDIAMemoryBusWidthIsExact(t *testing.T) {
	if got := knownNVIDIAMemoryBusWidth("NVIDIA GeForce RTX 4060 Ti"); got != 128 {
		t.Fatalf("4060 Ti width=%v", got)
	}
	if got := knownNVIDIAMemoryBusWidth("NVIDIA GeForce RTX 4060 Ti Laptop GPU"); got != 0 {
		t.Fatalf("laptop variant must not inherit desktop width: %v", got)
	}
}
