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
)

func processEnvironValue(t *testing.T, pid int, key string) string {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range strings.Split(string(data), "\x00") {
		name, value, found := strings.Cut(entry, "=")
		if found && name == key {
			return value
		}
	}
	return ""
}

func processCmdline(t *testing.T, pid int) []string {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
}

func flagValues(args []string, flag string) []string {
	var values []string
	for index, arg := range args {
		if arg == flag && index+1 < len(args) {
			values = append(values, args[index+1])
		}
	}
	return values
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func TestAppendPlacementLaunchArgsIsolatesSingleGPU(t *testing.T) {
	got, env := appendPlacementLaunchArgs(nil, []string{"CUDA1"}, "1,1", false)
	want := []string{"--device", "CUDA0", "--split-mode", "none"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("single GPU=%v want=%v", got, want)
	}
	if strings.Join(env, "|") != "CUDA_VISIBLE_DEVICES=1" {
		t.Fatalf("env=%v want CUDA_VISIBLE_DEVICES=1", env)
	}
	args, env := appendPlacementLaunchArgs(nil, nil, "1,1", false)
	if len(args) != 0 || env != nil {
		t.Fatal("empty device list must not emit placement flags")
	}
}

func TestAppendPlacementLaunchArgsLeavesMultiGPUSplitUnpinned(t *testing.T) {
	got, env := appendPlacementLaunchArgs(nil, []string{"CUDA0", "CUDA1"}, "3,1", false)
	want := []string{"--device", "CUDA0,CUDA1", "--tensor-split", "3,1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("multi GPU=%v want=%v", got, want)
	}
	if env != nil {
		t.Fatalf("multi GPU must not isolate devices: %v", env)
	}
	got, env = appendPlacementLaunchArgs(nil, []string{"CUDA0", "CUDA1"}, "3,1", true)
	want = []string{"--device", "CUDA0,CUDA1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("multi GPU override=%v want=%v", got, want)
	}
	if env != nil {
		t.Fatalf("multi GPU override must not isolate devices: %v", env)
	}
}

func TestIsolateSingleVisibleGPU(t *testing.T) {
	llama, env, ok := isolateSingleVisibleGPU("CUDA1")
	if !ok || llama != "CUDA0" || strings.Join(env, "|") != "CUDA_VISIBLE_DEVICES=1" {
		t.Fatalf("cuda=%s env=%v ok=%v", llama, env, ok)
	}
	llama, env, ok = isolateSingleVisibleGPU("ROCm2")
	if !ok || llama != "ROCm0" || strings.Join(env, "|") != "HIP_VISIBLE_DEVICES=2|ROCR_VISIBLE_DEVICES=2" {
		t.Fatalf("rocm=%s env=%v ok=%v", llama, env, ok)
	}
	if _, _, ok := isolateSingleVisibleGPU("0"); ok {
		t.Fatal("numeric backends must stay unmapped")
	}
}

func TestSingleGPUPlacementIsolatesVisibleDevice(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, model, sup, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 4 * testGiB}, {ID: "CUDA1", FreeBytes: 16 * testGiB},
	}}}}
	enabled, autoload, eviction := true, true, true
	instance, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: model.ID, Name: "Pinned companions", Enabled: &enabled, Autoload: &autoload,
		Priority: "normal", EvictionEnabled: &eviction, GPUMode: "auto",
		Options: map[string]string{
			"mmproj":           "/models/mmproj.gguf",
			"spec-draft-model": "/models/draft.gguf",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	args := processCmdline(t, sup.Status(instance.ID).PID)
	if got := flagValues(args, "--device"); len(got) != 1 || got[0] != "CUDA0" {
		t.Fatalf("device=%v want CUDA0 (CUDA1 isolated via CUDA_VISIBLE_DEVICES)", got)
	}
	if got := flagValues(args, "--split-mode"); len(got) != 1 || got[0] != "none" {
		t.Fatalf("split-mode=%v want none", got)
	}
	if hasFlag(args, "--mmproj-device") || hasFlag(args, "--spec-draft-device") || hasFlag(args, "--tensor-split") {
		t.Fatalf("companion device flags must stay unset: %v", args)
	}
	if got := flagValues(args, "--mmproj"); len(got) != 1 || got[0] != "/models/mmproj.gguf" {
		t.Fatalf("mmproj path=%v", got)
	}
	if got := flagValues(args, "--spec-draft-model"); len(got) != 1 || got[0] != "/models/draft.gguf" {
		t.Fatalf("spec-draft-model path=%v", got)
	}
	if got := processEnvironValue(t, sup.Status(instance.ID).PID, "CUDA_VISIBLE_DEVICES"); got != "1" {
		t.Fatalf("CUDA_VISIBLE_DEVICES=%q want 1", got)
	}
}

func TestSingleGPUPlacementOmitsCompanionDeviceFlagsWithoutCompanions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, model, sup, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 4 * testGiB}, {ID: "CUDA1", FreeBytes: 16 * testGiB},
	}}}}
	enabled, autoload, eviction := true, true, true
	instance, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: model.ID, Name: "No companions", Enabled: &enabled, Autoload: &autoload,
		Priority: "normal", EvictionEnabled: &eviction, GPUMode: "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	args := processCmdline(t, sup.Status(instance.ID).PID)
	if got := flagValues(args, "--device"); len(got) != 1 || got[0] != "CUDA0" {
		t.Fatalf("device=%v want CUDA0 (CUDA1 isolated via CUDA_VISIBLE_DEVICES)", got)
	}
	if got := flagValues(args, "--split-mode"); len(got) != 1 || got[0] != "none" {
		t.Fatalf("split-mode=%v want none", got)
	}
	if hasFlag(args, "--mmproj-device") || hasFlag(args, "--spec-draft-device") || hasFlag(args, "--tensor-split") {
		t.Fatalf("companion/split flags must be absent: %v", args)
	}
	if got := processEnvironValue(t, sup.Status(instance.ID).PID, "CUDA_VISIBLE_DEVICES"); got != "1" {
		t.Fatalf("CUDA_VISIBLE_DEVICES=%q want 1", got)
	}
}

func TestMultiGPUVRAMSplitDoesNotPinCompanionDevices(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, model, sup, exec := setupLifecycle(t, true, false)
	exec("UPDATE models SET total_bytes=? WHERE id=?", 14*testGiB, model.ID)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 10 * testGiB}, {ID: "CUDA1", FreeBytes: 9 * testGiB},
	}}}}
	enabled, autoload, eviction := true, true, true
	instance, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: model.ID, Name: "VRAM split", Enabled: &enabled, Autoload: &autoload,
		Priority: "normal", EvictionEnabled: &eviction, GPUMode: "auto",
		Options: map[string]string{
			"mmproj":           "/models/mmproj.gguf",
			"spec-draft-model": "/models/draft.gguf",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	args := processCmdline(t, sup.Status(instance.ID).PID)
	if got := flagValues(args, "--device"); len(got) != 1 || got[0] != "CUDA0,CUDA1" {
		t.Fatalf("device=%v want CUDA0,CUDA1", got)
	}
	if got := flagValues(args, "--tensor-split"); len(got) != 1 || got[0] == "" {
		t.Fatalf("tensor-split=%v want a placement split", got)
	}
	if hasFlag(args, "--split-mode") || hasFlag(args, "--mmproj-device") || hasFlag(args, "--spec-draft-device") {
		t.Fatalf("VRAM split must not pin companions or force split-mode none: %v", args)
	}
	if got := processEnvironValue(t, sup.Status(instance.ID).PID, "CUDA_VISIBLE_DEVICES"); got != "" {
		t.Fatalf("VRAM split must not set CUDA_VISIBLE_DEVICES, got %q", got)
	}
}
