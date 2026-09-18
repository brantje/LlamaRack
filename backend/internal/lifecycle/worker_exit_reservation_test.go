package lifecycle

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestUnexpectedReadyWorkerExitReleasesCommittedReservation(t *testing.T) {
	ctx := context.Background()
	s, ms, model, sup, execDB := setupLifecycle(t, true, false)
	path, err := ms.ModelAbsolutePath(model)
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleMetadataGGUF(t, path, "qwen2", map[string]int64{
		"qwen2.context_length": 32768, "qwen2.block_count": 10, "qwen2.embedding_length": 1024,
		"qwen2.attention.head_count": 8, "qwen2.attention.head_count_kv": 8,
	})
	items, err := s.instances.ListByModel(ctx, model.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 10*testGiB, model.ID)
	spill, eviction := true, false
	instance, err = s.instances.Update(ctx, instance.ID, instances.UpdateInput{
		Name: instance.Name, SystemSpilloverEnabled: &spill, EvictionEnabled: &eviction,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{
		RAMTotalBytes: 64 * testGiB, RAMAvailableBytes: 64 * testGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 8 * testGiB, FreeBytes: 8 * testGiB}},
	}}}
	s.SetProfileGetter(func() (llamacpp.Profile, error) { return spilloverProfile(), nil })

	if _, err := s.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}
	rt := sup.Status(instance.ID)
	if rt.State != supervisor.Ready || rt.PID <= 0 {
		t.Fatalf("runtime=%+v", rt)
	}
	lease, ok := s.reservations.GetByInstance(instance.ID)
	if !ok || lease.State != "committed" || lease.HostRAM <= 0 || len(lease.GPUs) == 0 {
		t.Fatalf("committed spill lease=%+v ok=%v", lease, ok)
	}
	proc, err := os.FindProcess(rt.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if sup.Status(instance.ID).State == supervisor.Failed {
			if _, exists := s.reservations.Get(lease.ID); !exists {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, exists := s.reservations.Get(lease.ID)
	t.Fatalf("unexpected exit stranded lease=%+v exists=%v runtime=%+v", current, exists, sup.Status(instance.ID))
}
