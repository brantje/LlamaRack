package lifecycle

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/recommendations"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

func TestEffectiveContextChangesSchedulerDemand(t *testing.T) {
	ctx := context.Background()
	s, ms, m, _, exec := setupLifecycle(t, true, false)
	path, err := ms.ModelAbsolutePath(m)
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleMetadataGGUF(t, path, "qwen2", map[string]int64{
		"qwen2.context_length": 131072, "qwen2.block_count": 32, "qwen2.embedding_length": 4096,
		"qwen2.attention.head_count": 32, "qwen2.attention.head_count_kv": 8,
	})
	exec("UPDATE models SET total_bytes=? WHERE id=?", 4*testGiB, m.ID)
	m, err = ms.GetByID(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	inherited := items[0]
	enabled := true
	large, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: m.ID, Name: "Large context", Enabled: &enabled,
		Options: map[string]string{"ctx-size": "32768"},
	})
	if err != nil {
		t.Fatal(err)
	}

	smallOpts, err := s.resolveLaunchOptions(ctx, m.ID, inherited.ID)
	if err != nil {
		t.Fatal(err)
	}
	largeOpts, err := s.resolveLaunchOptions(ctx, m.ID, large.ID)
	if err != nil {
		t.Fatal(err)
	}
	small := s.estimateDemand(m, path, smallOpts)
	big := s.estimateDemand(m, path, largeOpts)
	if small.KVCacheBytes <= 0 || big.KVCacheBytes <= small.KVCacheBytes {
		t.Fatalf("context demand small=%+v large=%+v opts small=%v large=%v", small, big, smallOpts["ctx-size"], largeOpts["ctx-size"])
	}
	if small.VRAMBytes() >= big.VRAMBytes() {
		t.Fatalf("large context should increase VRAM demand")
	}

	rec := recommendations.Analyze(m, path, hardware.Snapshot{}, 32768, nil)
	if rec.Memory.KVCacheBytes != big.KVCacheBytes || rec.Memory.RuntimeOverheadBytes != big.RuntimeOverheadBytes {
		t.Fatalf("analyzer=%+v lifecycle=%+v", rec.Memory, big)
	}
}

func TestPlacementRejectsFileSizeFitButNotRuntimeDemand(t *testing.T) {
	ctx := context.Background()
	s, ms, m, _, exec := setupLifecycle(t, true, false)
	exec("UPDATE models SET total_bytes=? WHERE id=?", testGiB, m.ID)
	m, err := ms.GetByID(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	path, err := ms.ModelAbsolutePath(m)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	options, err := s.resolveLaunchOptions(ctx, m.ID, items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	demand := s.estimateDemand(m, path, options)
	if demand.VRAMBytes() <= m.TotalBytes {
		t.Fatalf("runtime demand %d should exceed file size %d", demand.VRAMBytes(), m.TotalBytes)
	}
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 1600 * 1024 * 1024}}}
	fileFit, err := scheduler.PlanPlacement(snapshot, scheduler.PlacementRequest{RequiredBytes: m.TotalBytes})
	if err != nil || !fileFit.Fits {
		t.Fatalf("file size should fit: %+v err=%v", fileFit, err)
	}
	runtimeFit, err := scheduler.PlanPlacement(snapshot, scheduler.PlacementRequest{RequiredBytes: demand.VRAMBytes()})
	if err != nil || runtimeFit.Fits {
		t.Fatalf("runtime demand should not fit: %+v err=%v demand=%d", runtimeFit, err, demand.VRAMBytes())
	}
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{snapshot}}
	if _, err := s.StartInstance(ctx, items[0].ID); err == nil {
		t.Fatal("start should reject runtime demand that exceeds usable VRAM")
	}
}

func TestCompanionWeightsIncreaseDemand(t *testing.T) {
	s, ms, m, _, _ := setupLifecycle(t, true, false)
	path, err := ms.ModelAbsolutePath(m)
	if err != nil {
		t.Fatal(err)
	}
	companion := filepath.Join(t.TempDir(), "mmproj.gguf")
	if err := os.WriteFile(companion, make([]byte, 32*1024*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	base := s.estimateDemand(m, path, map[string]string{"ctx-size": "4096"})
	with := s.estimateDemand(m, path, map[string]string{"ctx-size": "4096", "mmproj": companion})
	if with.WeightsBytes != base.WeightsBytes+32*1024*1024 {
		t.Fatalf("companion weights base=%d with=%d", base.WeightsBytes, with.WeightsBytes)
	}
	missing := s.estimateDemand(m, path, map[string]string{"ctx-size": "4096", "mmproj": filepath.Join(t.TempDir(), "nope.gguf"), "spec-draft-model": t.TempDir()})
	if missing.WeightsBytes != base.WeightsBytes {
		t.Fatalf("missing/dir companions should be ignored: %d vs %d", missing.WeightsBytes, base.WeightsBytes)
	}
	if companionBytes(nil) != 0 {
		t.Fatal("nil options")
	}
}

func TestEvictionPlanUsesRuntimeDemand(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, exec := setupLifecycle(t, true, false)
	items, err := s.instances.ListByModel(ctx, m.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	if _, err := s.StartInstance(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE models SET total_bytes=? WHERE id=?", 2*testGiB, m.ID)
	plan, err := s.EvictionPlan(ctx, testGiB)
	if err != nil || !plan.Fits || len(plan.Evict) != 1 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if plan.Evict[0].EstimatedBytes <= 2*testGiB {
		t.Fatalf("eviction should use runtime demand above file size, got %d", plan.Evict[0].EstimatedBytes)
	}
}

func TestDemandCompatibleWithMultiGPULease(t *testing.T) {
	demand := scheduler.EstimateDemand(scheduler.DemandInput{WeightsBytes: 14 * testGiB, Context: 4096})
	s, _, _, _, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 10 * testGiB},
		{ID: "CUDA1", FreeBytes: 9 * testGiB},
	}}}}
	placement, err := s.preparePlacementWithDemand(context.Background(), instances.Instance{ID: "wide", GPUMode: "auto"}, demand, false)
	if err != nil || !placement.Fits || len(placement.Devices) != 2 {
		t.Fatalf("placement=%+v err=%v demand=%d", placement, err, demand.VRAMBytes())
	}
	lease, ok := s.reservations.GetByInstance("wide")
	if !ok || len(lease.GPUs) != 2 {
		t.Fatalf("lease=%+v ok=%v", lease, ok)
	}
}

func writeLifecycleMetadataGGUF(t *testing.T, path, architecture string, ints map[string]int64) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	mustWriteBinary(t, &b, uint32(3))
	mustWriteBinary(t, &b, uint64(0))
	mustWriteBinary(t, &b, uint64(1+len(ints)))
	writeGGUFString(t, &b, "general.architecture")
	mustWriteBinary(t, &b, uint32(8))
	writeGGUFString(t, &b, architecture)
	for key, value := range ints {
		writeGGUFString(t, &b, key)
		mustWriteBinary(t, &b, uint32(11))
		mustWriteBinary(t, &b, value)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeGGUFString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	mustWriteBinary(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func mustWriteBinary(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
