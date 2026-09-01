package lifecycle

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
)

func processTensorSplits(t *testing.T, pid int) []string {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	var values []string
	for index, arg := range args {
		if arg == "--tensor-split" && index+1 < len(args) {
			values = append(values, args[index+1])
		}
	}
	return values
}

func tensorSplitProfile() llamacpp.Profile {
	return llamacpp.Profile{Path: "/app/llama-server", Version: "test", Options: []llamacpp.Option{
		{Key: "tensor-split", ValueHint: "SPLIT", Kind: "string"},
	}}
}

func TestLlamaTensorSplitOverrideWinsOverPlacementSplit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, model, sup, _ := setupLifecycle(t, true, false)
	s.SetProfileGetter(func() (llamacpp.Profile, error) { return tensorSplitProfile(), nil })
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 8 * testGiB}, {ID: "CUDA1", FreeBytes: 8 * testGiB},
	}}}}
	enabled, autoload, eviction := true, true, true
	instance, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: model.ID, Name: "Override split", Enabled: &enabled, Autoload: &autoload,
		Priority: "normal", EvictionEnabled: &eviction, GPUMode: "manual",
		GPUDevices: []string{"CUDA0", "CUDA1"}, TensorSplit: "1,1",
		Options: map[string]string{"tensor-split": "3,1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	values := processTensorSplits(t, sup.Status(instance.ID).PID)
	if len(values) != 1 || values[0] != "3,1" {
		t.Fatalf("tensor split args=%v; want only explicit llama.cpp override 3,1", values)
	}
}

func TestPlacementTensorSplitIsUsedWithoutLlamaOverride(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, model, sup, _ := setupLifecycle(t, true, false)
	s.SetProfileGetter(func() (llamacpp.Profile, error) { return tensorSplitProfile(), nil })
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 8 * testGiB}, {ID: "CUDA1", FreeBytes: 8 * testGiB},
	}}}}
	enabled, autoload, eviction := true, true, true
	instance, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: model.ID, Name: "Placement split", Enabled: &enabled, Autoload: &autoload,
		Priority: "normal", EvictionEnabled: &eviction, GPUMode: "manual",
		GPUDevices: []string{"CUDA0", "CUDA1"}, TensorSplit: "1,1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	values := processTensorSplits(t, sup.Status(instance.ID).PID)
	if len(values) != 1 || values[0] != "1,1" {
		t.Fatalf("tensor split args=%v; want manager placement split 1,1", values)
	}
}
