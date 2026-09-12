package benchmark

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

func TestBenchmarkServiceBaselineUsesInstanceDerivedEffectiveConfig(t *testing.T) {
	executor := benchmarkTestExecutor{output: RunOutput{MachineOutput: []byte(`[{"n_prompt":512,"avg_ts":123.4}]`)}}
	s, store, ledger, _, cfg := testBenchmarkService(t, executor)
	cfg.effective.Values["batch-size"] = "512"
	cfg.effective.Sources["batch-size"] = "instance"
	caps := runtimeTestCapabilities(
		llamacpp.Option{Key: "n-gpu-layers", Kind: "integer"},
		llamacpp.Option{Key: "batch-size", Kind: "integer"},
	)
	s.discoverCapabilities = func(context.Context, string) (Capabilities, error) { return caps, nil }
	run, err := s.CreateWithOverrides(context.Background(), "instance-1", nil, RuntimeOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(RuntimeOverrideKeys(run.BenchmarkOverrides)) != 0 {
		t.Fatalf("baseline overrides=%+v", run.BenchmarkOverrides)
	}
	if run.InstanceConfig.Options["batch-size"] != "512" || run.EffectiveConfig.Options["batch-size"] != "512" {
		t.Fatalf("instance=%+v effective=%+v", run.InstanceConfig, run.EffectiveConfig)
	}
	if !strings.Contains(strings.Join(run.ResolvedArgv, " "), "--batch-size 512") {
		t.Fatalf("argv=%v", run.ResolvedArgv)
	}
	select {
	case <-store.terminal:
	case <-time.After(2 * time.Second):
		t.Fatal("benchmark did not finish")
	}
	waitBenchmarkReservationReleased(t, ledger, run.ID)
}

func TestBenchmarkServiceBaselinePersistsInstanceOverridesAndEffectiveConfigSeparately(t *testing.T) {
	executor := benchmarkTestExecutor{output: RunOutput{MachineOutput: []byte(`[{"n_prompt":512,"avg_ts":123.4}]`)}}
	s, store, ledger, _, _ := testBenchmarkService(t, executor)
	caps := runtimeTestCapabilities(
		llamacpp.Option{Key: "n-gpu-layers", Kind: "integer"},
		llamacpp.Option{Key: "batch-size", Kind: "integer"},
	)
	s.discoverCapabilities = func(context.Context, string) (Capabilities, error) { return caps, nil }
	run, err := s.CreateWithOverrides(context.Background(), "instance-1", nil, RuntimeOverrides{BatchSize: intp(1024)})
	if err != nil {
		t.Fatal(err)
	}
	if run.InstanceConfig.Options["n-gpu-layers"] != "0" || run.InstanceConfig.Options["batch-size"] != "" {
		t.Fatalf("Instance snapshot=%+v", run.InstanceConfig)
	}
	if run.BenchmarkOverrides.BatchSize == nil || *run.BenchmarkOverrides.BatchSize != 1024 {
		t.Fatalf("overrides=%+v", run.BenchmarkOverrides)
	}
	if run.EffectiveConfig.Options["batch-size"] != "1024" || run.EffectiveConfig.Options["threads"] != "4" {
		t.Fatalf("effective=%+v", run.EffectiveConfig)
	}
	if !strings.Contains(strings.Join(run.ResolvedArgv, " "), "--batch-size 1024") {
		t.Fatalf("argv=%v", run.ResolvedArgv)
	}
	select {
	case <-store.terminal:
	case <-time.After(2 * time.Second):
		t.Fatal("benchmark did not finish")
	}
	waitBenchmarkReservationReleased(t, ledger, run.ID)
}

func TestBenchmarkServiceOverrideDrivesResourceAdmission(t *testing.T) {
	s, _, _, _, cfg := testBenchmarkService(t, benchmarkTestExecutor{})
	cfg.effective.Values["n-gpu-layers"] = "0"
	caps := runtimeTestCapabilities(llamacpp.Option{Key: "n-gpu-layers", Kind: "integer"})
	s.discoverCapabilities = func(context.Context, string) (Capabilities, error) { return caps, nil }
	// The baseline is CPU-only and can be admitted on a host without GPUs. The
	// override moves model demand to GPU and must therefore fail admission.
	if _, err := s.CreateWithOverrides(context.Background(), "instance-1", nil, RuntimeOverrides{GPULayers: intp(-1)}); !errors.Is(err, ErrInsufficientResources) {
		t.Fatalf("override admission err=%v", err)
	}
}

func TestBenchmarkServiceGPUDeviceOverrideControlsReservationAndArgv(t *testing.T) {
	wait := make(chan struct{})
	s, _, ledger, inst, cfg := testBenchmarkService(t, benchmarkTestExecutor{wait: wait})
	inst.item.GPUMode = "auto"
	cfg.effective.Values["n-gpu-layers"] = "-1"
	cfg.effective.Sources["n-gpu-layers"] = "instance"
	s.hardware = benchmarkTestHardware{snapshot: hardware.Snapshot{
		GPUs:          []hardware.GPU{{ID: "CUDA0", TotalBytes: 2 << 30, FreeBytes: 2 << 30}, {ID: "CUDA1", TotalBytes: 2 << 30, FreeBytes: 2 << 30}},
		RAMTotalBytes: 4 << 30, RAMAvailableBytes: 4 << 30,
	}}
	caps := runtimeTestCapabilities(
		llamacpp.Option{Key: "n-gpu-layers", Kind: "integer"},
		llamacpp.Option{Key: "device", Kind: "string"},
		llamacpp.Option{Key: "tensor-split", Kind: "string"},
	)
	s.discoverCapabilities = func(context.Context, string) (Capabilities, error) { return caps, nil }
	run, err := s.CreateWithOverrides(context.Background(), "instance-1", nil, RuntimeOverrides{GPUDevices: slicep([]string{"CUDA1"}), TensorSplit: strp("1")})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Hardware.SelectedDevices) != 1 || run.Hardware.SelectedDevices[0] != "CUDA1" {
		t.Fatalf("selected=%v", run.Hardware.SelectedDevices)
	}
	if run.EffectiveConfig.GPUMode != "manual" || len(run.EffectiveConfig.GPUDevices) != 1 || run.EffectiveConfig.GPUDevices[0] != "CUDA1" || run.EffectiveConfig.TensorSplit != "1" {
		t.Fatalf("effective=%+v", run.EffectiveConfig)
	}
	command := strings.Join(run.ResolvedArgv, " ")
	if !strings.Contains(command, "--device CUDA1") || !strings.Contains(command, "--tensor-split 1") {
		t.Fatalf("argv=%q", command)
	}
	if _, ok := ledger.GetByOwner(scheduler.ResourceOwner{Kind: scheduler.ResourceOwnerBenchmark, ID: run.ID}); !ok {
		t.Fatal("benchmark reservation missing")
	}
	close(wait)
	waitBenchmarkReservationReleased(t, ledger, run.ID)
}
