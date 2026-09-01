package observability

import (
	"context"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/telemetry"
)

func TestHardwareEdgePathsAndCompleteLifecycleSummary(t *testing.T) {
	ctx := context.Background()
	s := testService(t)

	empty := s.LatestHardware()
	if len(empty.Hardware.GPUs) != 0 || len(empty.Hardware.Processes) != 0 || len(empty.Telemetry) != 0 {
		t.Fatalf("empty latest hardware=%+v", empty)
	}

	snapshot := hardware.Snapshot{
		RAMTotalBytes:     100,
		RAMAvailableBytes: 200,
		GPUs:              []hardware.GPU{},
		Processes:         []hardware.GPUProcess{},
	}
	samples := []telemetry.Sample{{
		InstanceID: "empty-metrics",
		GPUs:       []telemetry.GPUUsage{{DeviceID: "CUDA0"}},
	}}
	if err := s.RecordHardware(ctx, snapshot, samples); err != nil {
		t.Fatal(err)
	}

	items, err := s.HardwareTimeseries(ctx, "ram_used_bytes", 0, 0, "", "")
	if err != nil || len(items) != 1 || items[0].Value != 0 {
		t.Fatalf("default bucket/since items=%+v err=%v", items, err)
	}
	items, err = s.HardwareTimeseries(ctx, "ram_total_bytes", time.Now().Add(-time.Minute).UnixMilli(), 48*3600, "", "")
	if err != nil || len(items) != 1 || items[0].Value != 100 {
		t.Fatalf("clamped bucket items=%+v err=%v", items, err)
	}

	for _, event := range []string{LifecycleAutoload, LifecycleLoad, LifecycleFailedStart, LifecycleEviction, LifecycleIdleUnload} {
		duration := time.Duration(0)
		if event == LifecycleLoad {
			duration = 250 * time.Millisecond
		}
		if err := s.RecordLifecycle(ctx, event, "edge-instance", duration); err != nil {
			t.Fatalf("record %s: %v", event, err)
		}
	}
	summary, err := s.LifecycleSummary(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Autoloads != 0 || summary.FailedStarts != 0 || summary.LoadMS != 0 || summary.Loads != 1 || summary.Evictions != 1 || summary.IdleUnloads != 1 {
		t.Fatalf("summary=%+v", summary)
	}

	if err := s.PruneHardware(ctx, 0); err != nil {
		t.Fatal(err)
	}
}
