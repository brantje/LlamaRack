package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/telemetry"
)

func int64p(value int64) *int64 { return &value }

func TestHardwarePersistenceTimeseriesLifecycleAndMetrics(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	now := time.Now().UTC().Truncate(time.Second)
	cpu := 12.5
	gpuUtil := 77.0
	snapshot := hardware.Snapshot{
		RAMTotalBytes: 64 << 30, RAMAvailableBytes: 40 << 30, CollectedAt: now,
		GPUs: []hardware.GPU{
			{ID:"CUDA0", Backend:"cuda", Index:0, Name:"GPU A", TotalBytes:24<<30, UsedBytes:12<<30, FreeBytes:12<<30, UtilizationPct:50},
			{ID:"CUDA1", Backend:"cuda", Index:1, Name:"GPU B", TotalBytes:24<<30, UsedBytes:6<<30, FreeBytes:18<<30, UtilizationPct:20},
		},
		Processes: []hardware.GPUProcess{{PID:123, DeviceID:"CUDA0", UsedBytes:4<<30, ProcessName:"llama-server"}},
	}
	samples := []telemetry.Sample{{
		InstanceID:"coder", PID:123, GPUDevices:[]string{"CUDA0"}, CollectedAt:now,
		VRAMUsedBytes:int64p(4<<30), GPUUtilizationPct:&gpuUtil, CPUPercent:&cpu, MemoryUsedBytes:int64p(2<<30),
		GPUs: []telemetry.GPUUsage{{DeviceID:"CUDA0", VRAMUsedBytes:int64p(4<<30), UtilizationPct:&gpuUtil}},
	}}
	if err := s.RecordHardware(ctx, snapshot, samples); err != nil { t.Fatal(err) }
	latest := s.LatestHardware()
	if len(latest.Hardware.GPUs) != 2 || len(latest.Telemetry) != 1 || latest.Telemetry[0].InstanceID != "coder" { t.Fatalf("latest=%+v", latest) }
	latest.Hardware.GPUs[0].Name = "changed"
	if s.LatestHardware().Hardware.GPUs[0].Name != "GPU A" { t.Fatal("latest snapshot must be copied") }

	for _, tc := range []struct{ metric, device, instance string; want int }{
		{"ram_used_bytes", "", "", 1}, {"vram_used_bytes", "", "", 1}, {"vram_used_bytes", "CUDA0", "", 1},
		{"gpu_utilization_pct", "CUDA1", "", 1}, {"instance_cpu_percent", "", "coder", 1}, {"instance_vram_used_bytes", "CUDA0", "coder", 1},
	} {
		items, err := s.HardwareTimeseries(ctx, tc.metric, now.Add(-time.Minute).UnixMilli(), 10, tc.device, tc.instance)
		if err != nil || len(items) != tc.want { t.Fatalf("%s/%s/%s items=%+v err=%v", tc.metric, tc.device, tc.instance, items, err) }
	}
	if _, err := s.HardwareTimeseries(ctx, "bogus", 0, 0, "", ""); err == nil { t.Fatal("expected unsupported hardware metric") }

	if err := s.RecordRequest(ctx, RequestRecord{StartedAt:now.UnixMilli(), FinishedAt:now.UnixMilli(), InstanceID:"coder", Endpoint:"/v1/chat/completions", StatusCode:200, Autoloaded:true, LoadDurationMS:125}); err != nil { t.Fatal(err) }
	if err := s.RecordRequest(ctx, RequestRecord{StartedAt:now.UnixMilli(), FinishedAt:now.UnixMilli(), InstanceID:"coder", Endpoint:"/v1/chat/completions", StatusCode:503, Autoloaded:true, LoadDurationMS:75}); err != nil { t.Fatal(err) }
	lifecycle, err := s.LifecycleSummary(ctx)
	if err != nil || lifecycle.Autoloads != 2 || lifecycle.FailedStarts != 1 || lifecycle.LoadMS != 200 { t.Fatalf("lifecycle=%+v err=%v", lifecycle, err) }

	h := NewManagementHandler(s)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/summary?window_seconds=60", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"hardware"`) || !strings.Contains(w.Body.String(), `"autoloads":2`) { t.Fatalf("summary=%d %s", w.Code, w.Body.String()) }
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/observability/timeseries?metric=vram_used_bytes&device_id=CUDA0&window_seconds=60&bucket_seconds=10", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "CUDA0") { t.Fatalf("hardware series=%d %s", w.Code, w.Body.String()) }

	metrics := NewMetricsHandler(s, nil)
	w = httptest.NewRecorder()
	metrics.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := w.Body.String()
	for _, want := range []string{"llamacpp_manager_ram_used_bytes", `device_id="CUDA0"`, "llamacpp_manager_instance_cpu_percent", "llamacpp_manager_autoload_total"} {
		if !strings.Contains(body, want) { t.Fatalf("metrics missing %q: %s", want, body) }
	}
}

func TestHardwarePruneAndRetentionStop(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	old := hardware.Snapshot{RAMTotalBytes:1024, RAMAvailableBytes:512, CollectedAt:time.Now().Add(-40*24*time.Hour), GPUs:[]hardware.GPU{}, Processes:[]hardware.GPUProcess{}}
	if err := s.RecordHardware(ctx, old, nil); err != nil { t.Fatal(err) }
	if err := s.PruneHardware(ctx, 30); err != nil { t.Fatal(err) }
	items, err := s.HardwareTimeseries(ctx, "ram_used_bytes", time.Now().Add(-60*24*time.Hour).UnixMilli(), 60, "", "")
	if err != nil || len(items) != 0 { t.Fatalf("old items=%+v err=%v", items, err) }

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func(){ s.RunHardwareRetention(cancelCtx, func(context.Context) int { return 30 }); close(done) }()
	select { case <-done: case <-time.After(time.Second): t.Fatal("hardware retention did not stop") }
}
