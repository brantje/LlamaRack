package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

type readySwitchHardware struct {
	sup             *supervisor.Supervisor
	victimID        string
	occupied, freed hardware.Snapshot
}

func (h readySwitchHardware) Snapshot(context.Context) (hardware.Snapshot, error) {
	if h.sup.Status(h.victimID).State == supervisor.Ready {
		return h.occupied, nil
	}
	return h.freed, nil
}

func TestAutoMoEAutoloadEvictsIdleDualGPUVictim(t *testing.T) {
	ctx := context.Background()
	s, ms, victimModel, sup, execDB := setupLifecycle(t, true, false)
	victims, err := s.instances.ListByModel(ctx, victimModel.ID)
	if err != nil || len(victims) != 1 {
		t.Fatalf("victim instances=%+v err=%v", victims, err)
	}
	victim := victims[0]
	enabled := true
	if _, err := s.instances.Update(ctx, victim.ID, instances.UpdateInput{
		ModelID: victimModel.ID, Name: victim.Name, Slug: victim.ID, Priority: "normal",
		Enabled: &enabled, Autoload: &enabled, EvictionEnabled: &enabled, GPUMode: "auto",
	}); err != nil {
		t.Fatal(err)
	}
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 18*testGiB, victimModel.ID)

	sixteen := []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 16 * testGiB, TotalBytes: 16 * testGiB, UsedBytes: 0},
		{ID: "CUDA1", FreeBytes: 16 * testGiB, TotalBytes: 16 * testGiB, UsedBytes: 0},
	}
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{
		RAMAvailableBytes: 64 * testGiB, RAMTotalBytes: 64 * testGiB, GPUs: sixteen,
	}}}
	if _, err := s.StartInstance(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	if got := sup.Status(victim.ID).State; got != supervisor.Ready {
		t.Fatalf("victim state=%s", got)
	}
	if lease, ok := s.reservations.GetByInstance(victim.ID); !ok || len(lease.GPUs) < 2 {
		t.Fatalf("victim lease must span both GPUs, lease=%+v ok=%v", lease, ok)
	}

	victimPath, err := ms.ModelAbsolutePath(victimModel)
	if err != nil {
		t.Fatal(err)
	}
	src := writeLifecycleMoEGGUF(t, 40, 64)
	dst := filepath.Join(filepath.Dir(victimPath), "tiel-moe.gguf")
	payload, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	tielModel, err := ms.Create(ctx, models.CreateModelInput{Name: "Tiel MoE", GGUFPath: "tiel-moe.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 20*testGiB, tielModel.ID)
	tiel, err := s.instances.Create(ctx, instances.CreateInput{
		ModelID: tielModel.ID, Name: "tiel-coder", Enabled: &enabled, Autoload: &enabled,
		EvictionEnabled: &enabled, GPUMode: "auto", Options: map[string]string{"ctx-size": "4096"},
	})
	if err != nil {
		t.Fatal(err)
	}

	s.SetProfileGetter(func() (llamacpp.Profile, error) {
		return llamacpp.Profile{Options: []llamacpp.Option{{Key: "n-cpu-moe"}, {Key: "cpu-moe"}, {Key: "n-gpu-layers"}, {Key: "no-kv-offload"}}}, nil
	})

	const reserve = int64(512 * 1024 * 1024)
	occupied := hardware.Snapshot{
		RAMAvailableBytes: 64 * testGiB,
		RAMTotalBytes:     64 * testGiB,
		GPUs: []hardware.GPU{
			{ID: "CUDA0", FreeBytes: reserve, TotalBytes: 16 * testGiB, UsedBytes: 16*testGiB - reserve},
			{ID: "CUDA1", FreeBytes: 9 * testGiB, TotalBytes: 16 * testGiB, UsedBytes: 7 * testGiB},
		},
		Processes: []hardware.GPUProcess{{
			PID: sup.Status(victim.ID).PID, DeviceID: "CUDA0", UsedBytes: 15 * testGiB,
		}},
	}
	s.hardware = readySwitchHardware{
		sup:      sup,
		victimID: victim.ID,
		occupied: occupied,
		freed: hardware.Snapshot{
			RAMAvailableBytes: 64 * testGiB,
			RAMTotalBytes:     64 * testGiB,
			GPUs:              sixteen,
		},
	}

	if _, err := s.StartInstance(ctx, tiel.ID); err != nil {
		t.Fatalf("tiel should evict idle dual-GPU victim: %v", err)
	}
	if got := sup.Status(victim.ID).State; got != supervisor.Unloaded {
		t.Fatalf("victim should be evicted, state=%s", got)
	}
	if got := sup.Status(tiel.ID).State; got != supervisor.Ready {
		t.Fatalf("tiel state=%s", got)
	}
}
