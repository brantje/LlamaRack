package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

func TestPrepareRuntimeLaunchCompatibilityFallbacks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := models.Model{ID: "model", TotalBytes: testGiB}
	instance := instances.Instance{ID: "instance", GPUMode: "auto"}

	plain := &Service{}
	plan, err := plain.prepareRuntimeLaunch(context.Background(), instance, model, path, map[string]string{"ctx-size": "4096"}, llamacpp.Profile{}, false)
	if err != nil || !plan.Fits || plan.Mode != "unmanaged" || plan.Demand.VRAMBytes() <= model.TotalBytes {
		t.Fatalf("no scheduler fallback plan=%+v err=%v", plan, err)
	}

	s, _, _, _, _ := setupLifecycle(t, true, false)
	s.hardware = &sequenceHardware{err: errors.New("probe unavailable")}
	plan, err = s.prepareRuntimeLaunch(context.Background(), instance, model, path, map[string]string{"ctx-size": "4096"}, llamacpp.Profile{}, false)
	if err != nil || !plan.Fits || plan.Mode != "unmanaged" {
		t.Fatalf("probe fallback plan=%+v err=%v", plan, err)
	}
}

func TestPrepareRuntimeLaunchPressureWithoutEviction(t *testing.T) {
	ctx := context.Background()
	s, ms, model, _, execDB := setupLifecycle(t, true, false)
	path, err := ms.ModelAbsolutePath(model)
	if err != nil {
		t.Fatal(err)
	}
	writeLifecycleMetadataGGUF(t, path, "qwen2", map[string]int64{
		"qwen2.context_length": 32768, "qwen2.block_count": 16, "qwen2.embedding_length": 2048,
		"qwen2.attention.head_count": 16, "qwen2.attention.head_count_kv": 8,
	})
	execDB("UPDATE models SET total_bytes=? WHERE id=?", 8*testGiB, model.ID)
	model, err = ms.GetByID(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.instances.ListByModel(ctx, model.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("instances=%+v err=%v", items, err)
	}
	instance := items[0]
	s.hardware = &sequenceHardware{snapshots: []hardware.Snapshot{{
		RAMTotalBytes: 16 * testGiB, RAMAvailableBytes: 12 * testGiB,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: testGiB}},
	}}}
	_, err = s.prepareRuntimeLaunch(ctx, instance, model, path, map[string]string{"ctx-size": "4096"}, spilloverProfile(), false)
	if err == nil || !errors.Is(err, errResourcePressureBlocked) || !strings.Contains(err.Error(), "insufficient usable VRAM") {
		t.Fatalf("pressure error=%v", err)
	}
}

func TestRuntimePressureErrorVariants(t *testing.T) {
	gpuOnly := runtimePressureError(scheduler.RuntimePlan{
		Demand: scheduler.ResourceDemand{GPU: []scheduler.GPUResourceDemand{{Bytes: 8 * testGiB}}},
		Placement: scheduler.Placement{AvailableBytes: testGiB},
	})
	if !errors.Is(gpuOnly, errResourcePressureBlocked) || strings.Contains(gpuOnly.Error(), "host RAM") {
		t.Fatalf("gpu pressure=%v", gpuOnly)
	}
	withHost := runtimePressureError(scheduler.RuntimePlan{
		Demand: scheduler.ResourceDemand{HostRAMBytes: 4 * testGiB, GPU: []scheduler.GPUResourceDemand{{Bytes: 8 * testGiB}}},
		Placement: scheduler.Placement{AvailableBytes: 2 * testGiB},
	})
	if !errors.Is(withHost, errResourcePressureBlocked) || !strings.Contains(withHost.Error(), "host RAM") {
		t.Fatalf("host pressure=%v", withHost)
	}
}
