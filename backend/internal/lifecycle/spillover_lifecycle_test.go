package lifecycle

import (
	"context"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/scheduler"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func spilloverProfile() llamacpp.Profile {
	return llamacpp.Profile{Options: []llamacpp.Option{
		{Key: "ctx-size"}, {Key: "n-gpu-layers"}, {Key: "no-kv-offload"},
		{Key: "n-cpu-moe"}, {Key: "cpu-moe"},
	}}
}

func TestSystemSpilloverOffPreservesGPUOrFail(t *testing.T) {
	ctx := context.Background()
	s, _, model, _, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, model.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]
	execDB("UPDATE instances SET eviction_enabled=0 WHERE id=?", instance.ID)
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 10*testGiB, model.ID)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{
		RAMAvailableBytes: 64 * testGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * testGiB}},
	}}}
	s.SetProfileGetter(func() (llamacpp.Profile, error) { return spilloverProfile(), nil })

	if _, err := s.StartInstance(ctx, instance.ID); err == nil || !strings.Contains(err.Error(), "insufficient usable VRAM") {
		t.Fatalf("spillover disabled start err=%v", err)
	}
}

func TestSystemSpilloverStartsWithEphemeralPartialPlan(t *testing.T) {
	ctx := context.Background()
	s, _, model, sup, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, model.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 10*testGiB, model.ID)
	spill := true
	eviction := false
	instance, err = s.instances.Update(ctx, instance.ID, instances.UpdateInput{
		Name: instance.Name, SystemSpilloverEnabled: &spill, EvictionEnabled: &eviction,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{
		RAMAvailableBytes: 64 * testGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * testGiB}},
	}}}
	s.SetProfileGetter(func() (llamacpp.Profile, error) { return spilloverProfile(), nil })

	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	if sup.Status(instance.ID).State != supervisor.Ready {
		t.Fatalf("state=%s", sup.Status(instance.ID).State)
	}
	lease, ok := s.reservations.GetByInstance(instance.ID)
	if !ok || lease.HostRAM <= 0 || len(lease.GPUs) != 1 {
		t.Fatalf("spill lease=%+v ok=%v", lease, ok)
	}
	args := processCmdline(t, sup.Status(instance.ID).PID)
	if !hasFlag(args, "--n-gpu-layers") {
		t.Fatalf("missing ephemeral layer spill: %v", args)
	}
	saved, err := s.instances.Options(ctx, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, persisted := saved["n-gpu-layers"]; persisted {
		t.Fatalf("ephemeral spill flag persisted: %v", saved)
	}
	if logs := strings.Join(s.Logs(instance.ID), "\n"); !strings.Contains(logs, "spillover partial") {
		t.Fatalf("missing spillover manager log: %s", logs)
	}
}

func TestSystemSpilloverCanFallBackCPUOnlyWithoutGPUReservation(t *testing.T) {
	ctx := context.Background()
	s, ms, model, sup, execDB := setupLifecycle(t, true, false)
	path, err := ms.ModelAbsolutePath(model)
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleMetadataGGUF(t, path, "qwen2", map[string]int64{
		"qwen2.context_length": 262144, "qwen2.block_count": 10, "qwen2.embedding_length": 1024,
		"qwen2.attention.head_count": 8, "qwen2.attention.head_count_kv": 8,
	})
	items, _ := s.instances.ListByModel(ctx, model.ID)
	instance := items[0]
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 20*testGiB, model.ID)
	spill := true
	eviction := false
	instance, err = s.instances.Update(ctx, instance.ID, instances.UpdateInput{
		Name: instance.Name, SystemSpilloverEnabled: &spill, EvictionEnabled: &eviction,
		Options: map[string]string{"ctx-size": "262144"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{
		RAMAvailableBytes: 64 * testGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 2 * testGiB}},
	}}}
	s.SetProfileGetter(func() (llamacpp.Profile, error) { return spilloverProfile(), nil })

	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	lease, ok := s.reservations.GetByInstance(instance.ID)
	if !ok || len(lease.GPUs) != 0 || lease.HostRAM <= 20*testGiB {
		t.Fatalf("cpu lease=%+v ok=%v", lease, ok)
	}
	args := processCmdline(t, sup.Status(instance.ID).PID)
	values := flagValues(args, "--n-gpu-layers")
	if len(values) != 1 || values[0] != "0" {
		t.Fatalf("cpu-only args=%v", args)
	}
	if got := processEnvironValue(t, sup.Status(instance.ID).PID, "CUDA_VISIBLE_DEVICES"); got != "" {
		t.Fatalf("CPU-only worker should have empty CUDA visibility, got %q", got)
	}
}

func TestManualSystemSpilloverStaysOnConfiguredDevice(t *testing.T) {
	ctx := context.Background()
	s, _, model, sup, execDB := setupLifecycle(t, true, false)
	items, _ := s.instances.ListByModel(ctx, model.ID)
	instance := items[0]
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 6*testGiB, model.ID)
	spill := true
	eviction := false
	instance, err := s.instances.Update(ctx, instance.ID, instances.UpdateInput{
		Name: instance.Name, SystemSpilloverEnabled: &spill, EvictionEnabled: &eviction,
		GPUMode: "manual", GPUDevices: []string{"CUDA0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{
		RAMAvailableBytes: 64 * testGiB,
		GPUs: []hardware.GPU{
			{ID: "CUDA0", FreeBytes: 4 * testGiB},
			{ID: "CUDA1", FreeBytes: 16 * testGiB},
		},
	}}}
	s.SetProfileGetter(func() (llamacpp.Profile, error) { return spilloverProfile(), nil })
	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	lease, ok := s.reservations.GetByInstance(instance.ID)
	if !ok {
		t.Fatal("missing lease")
	}
	for _, gpu := range lease.GPUs {
		if gpu.DeviceID != "CUDA0" {
			t.Fatalf("manual spill substituted GPU: %+v", lease.GPUs)
		}
	}
	if sup.Status(instance.ID).State != supervisor.Ready {
		t.Fatalf("state=%s", sup.Status(instance.ID).State)
	}
}

func TestRuntimeCapabilitiesFollowDiscoveredProfile(t *testing.T) {
	profile := spilloverProfile()
	caps := runtimeCapabilities(profile)
	if !caps.GPULayers || !caps.NoKVOffload || !caps.NCPUMoe || !caps.CPUMoe {
		t.Fatalf("caps=%+v", caps)
	}
	if caps := runtimeCapabilities(llamacpp.Profile{}); caps != (scheduler.RuntimeCapabilities{}) {
		t.Fatalf("empty profile caps=%+v", caps)
	}
}
