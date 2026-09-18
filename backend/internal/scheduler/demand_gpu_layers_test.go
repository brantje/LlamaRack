package scheduler

import "testing"

func TestEstimateDemandNegativeGPULayersMeansFullOffload(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	full := EstimateDemand(DemandInput{WeightsBytes: gib, Options: map[string]string{"n-gpu-layers": "-1"}})
	cpu := EstimateDemand(DemandInput{WeightsBytes: gib, Options: map[string]string{"n-gpu-layers": "0"}})

	if full.VRAMBytes() <= 0 || full.HostRAMBytes != 0 {
		t.Fatalf("negative gpu layers should fully offload: %+v", full)
	}
	if cpu.VRAMBytes() != 0 || cpu.HostRAMBytes <= 0 {
		t.Fatalf("zero gpu layers should remain CPU-only: %+v", cpu)
	}

	fraction, cpuOnly := gpuOffloadFraction(map[string]string{"gpu-layers": "-1"}, 32)
	if fraction != 1 || cpuOnly {
		t.Fatalf("negative gpu layers fraction=%v cpuOnly=%v", fraction, cpuOnly)
	}
}
