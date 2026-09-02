package lifecycle

import (
	"context"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/scheduler"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestPreparePlacementManualDoesNotEvictUnrelatedGPU(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	cuda0 := items[0]
	enabled := true
	cuda1, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "cuda1-victim", Enabled: &enabled, Priority: "low",
		GPUMode: "manual", GPUDevices: []string{"CUDA1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.instances.Update(ctx, cuda0.ID, instances.UpdateInput{
		ModelID: m.ID, Name: cuda0.Name, Slug: cuda0.ID, Priority: "low", GPUMode: "manual", GPUDevices: []string{"CUDA0"},
	}); err != nil {
		t.Fatal(err)
	}

	s.hardware = abundantTwoGPUHardware()
	if _, err := s.StartInstance(ctx, cuda1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, cuda0.ID); err != nil {
		t.Fatal(err)
	}
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, m.ID)

	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{
		{
			GPUs: []hardware.GPU{
				{ID: "CUDA0", FreeBytes: testGiB},
				{ID: "CUDA1", FreeBytes: 20 * testGiB},
			},
			Processes: []hardware.GPUProcess{
				{PID: sup.Status(cuda0.ID).PID, DeviceID: "CUDA0", UsedBytes: 8 * testGiB},
				{PID: sup.Status(cuda1.ID).PID, DeviceID: "CUDA1", UsedBytes: 12 * testGiB},
			},
		},
		{GPUs: []hardware.GPU{
			{ID: "CUDA0", FreeBytes: 10 * testGiB},
			{ID: "CUDA1", FreeBytes: 20 * testGiB},
		}},
	}}

	placement, err := s.preparePlacement(ctx, instances.Instance{
		ID: "target", GPUMode: "manual", GPUDevices: []string{"CUDA0"},
	}, 6*testGiB)
	if err != nil {
		t.Fatal(err)
	}
	if !placement.Fits || len(placement.Devices) != 1 || placement.Devices[0] != "CUDA0" {
		t.Fatalf("placement=%+v", placement)
	}
	if got := sup.Status(cuda0.ID).State; got != supervisor.Unloaded {
		t.Fatalf("CUDA0 victim should be stopped: %s", got)
	}
	if got := sup.Status(cuda1.ID).State; got != supervisor.Ready {
		t.Fatalf("CUDA1 victim must stay running: %s", got)
	}
}

func TestPreparePlacementReplansAfterInaccurateVictimEstimate(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, execDB := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	first := items[0]
	enabled := true
	second, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "cuda0-second", Enabled: &enabled, Priority: "low",
		GPUMode: "manual", GPUDevices: []string{"CUDA0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "cuda1-unrelated", Enabled: &enabled, Priority: "low",
		GPUMode: "manual", GPUDevices: []string{"CUDA1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.instances.Update(ctx, first.ID, instances.UpdateInput{
		ModelID: m.ID, Name: first.Name, Slug: first.ID, Priority: "low", GPUMode: "manual", GPUDevices: []string{"CUDA0"},
	}); err != nil {
		t.Fatal(err)
	}

	s.hardware = abundantTwoGPUHardware()
	if _, err := s.StartInstance(ctx, unrelated.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartInstance(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	execDB("UPDATE models SET total_bytes=? WHERE id=?", testGiB, m.ID)

	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{
		{
			GPUs: []hardware.GPU{
				{ID: "CUDA0", FreeBytes: testGiB},
				{ID: "CUDA1", FreeBytes: 20 * testGiB},
			},
			Processes: []hardware.GPUProcess{
				{PID: sup.Status(first.ID).PID, DeviceID: "CUDA0", UsedBytes: testGiB},
				{PID: sup.Status(second.ID).PID, DeviceID: "CUDA0", UsedBytes: 8 * testGiB},
				{PID: sup.Status(unrelated.ID).PID, DeviceID: "CUDA1", UsedBytes: 12 * testGiB},
			},
		},
		{
			GPUs: []hardware.GPU{
				{ID: "CUDA0", FreeBytes: testGiB},
				{ID: "CUDA1", FreeBytes: 20 * testGiB},
			},
			Processes: []hardware.GPUProcess{
				{PID: sup.Status(second.ID).PID, DeviceID: "CUDA0", UsedBytes: 8 * testGiB},
				{PID: sup.Status(unrelated.ID).PID, DeviceID: "CUDA1", UsedBytes: 12 * testGiB},
			},
		},
		{GPUs: []hardware.GPU{
			{ID: "CUDA0", FreeBytes: 10 * testGiB},
			{ID: "CUDA1", FreeBytes: 20 * testGiB},
		}},
	}}

	placement, err := s.preparePlacement(ctx, instances.Instance{
		ID: "target", GPUMode: "manual", GPUDevices: []string{"CUDA0"},
	}, 6*testGiB)
	if err != nil {
		t.Fatal(err)
	}
	if !placement.Fits || placement.Devices[0] != "CUDA0" {
		t.Fatalf("placement=%+v", placement)
	}
	if got := sup.Status(unrelated.ID).State; got != supervisor.Ready {
		t.Fatalf("unrelated CUDA1 instance was stopped: %s", got)
	}
	if got := sup.Status(second.ID).State; got != supervisor.Unloaded {
		t.Fatalf("same-device victim should be stopped after re-plan: %s", got)
	}
}

func TestVictimPlacementCurrentRequiresObservedDeviceOverlap(t *testing.T) {
	ctx := context.Background()
	s, _, m, sup, _ := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	s.hardware = abundantSingleGPUHardware()
	if _, err := s.StartInstance(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	pid := sup.Status(items[0].ID).PID
	candidate := scheduler.Candidate{
		InstanceID: items[0].ID,
		Resources:  scheduler.CandidateResources{GPU: []scheduler.GPUResource{{DeviceID: "CUDA0", Bytes: testGiB}}},
	}
	if !s.victimPlacementCurrent(candidate, hardware.Snapshot{}) {
		t.Fatal("missing process info should keep the candidate")
	}
	if s.victimPlacementCurrent(candidate, hardware.Snapshot{Processes: []hardware.GPUProcess{{PID: pid, DeviceID: "CUDA1", UsedBytes: testGiB}}}) {
		t.Fatal("process on CUDA1 should not satisfy a CUDA0 plan")
	}
	if !s.victimPlacementCurrent(candidate, hardware.Snapshot{Processes: []hardware.GPUProcess{{PID: pid, DeviceID: "CUDA0", UsedBytes: testGiB}}}) {
		t.Fatal("same-device process should match")
	}
}

func abundantSingleGPUHardware() *sequenceHardware {
	return &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 24 * testGiB, TotalBytes: 24 * testGiB},
	}}}}
}

func abundantTwoGPUHardware() *sequenceHardware {
	return &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 24 * testGiB, TotalBytes: 24 * testGiB},
		{ID: "CUDA1", FreeBytes: 24 * testGiB, TotalBytes: 24 * testGiB},
	}}}}
}
