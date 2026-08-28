package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
	"github.com/brantje/llamacpp-manager/backend/internal/telemetry"
)

func TestSamplerPublishSubscribeCopiesAndFallback(t *testing.T) {
	service := testService(t)
	sampler := NewSampler(nil, service)
	if !sampler.Latest().CollectedAt.IsZero() { t.Fatal("new sampler should not have a sample") }
	if len(sampler.RuntimeStates()) != 0 { t.Fatal("new sampler should not have runtime states") }
	initial, events, cancel := sampler.Subscribe()
	if !initial.CollectedAt.IsZero() { t.Fatal("initial snapshot should be empty") }

	util := 33.0
	live := LiveSnapshot{
		Type:"observability", CollectedAt:time.Now().UTC(),
		Hardware:hardware.Snapshot{GPUs:[]hardware.GPU{{ID:"CUDA0", Name:"GPU", TotalBytes:100, UsedBytes:40, FreeBytes:60, UtilizationPct:util}}, Processes:[]hardware.GPUProcess{}},
		Telemetry:[]RuntimeTelemetrySample{{Sample:telemetry.Sample{InstanceID:"one", PID:1, GPUDevices:[]string{"CUDA0"}, CollectedAt:time.Now().UTC()}}},
	}
	sampler.publish(live)
	select {
	case got := <-events:
		if got.Type != "observability" || got.Hardware.GPUs[0].ID != "CUDA0" { t.Fatalf("got=%+v", got) }
	case <-time.After(time.Second): t.Fatal("no sampler event")
	}
	latest := sampler.Latest()
	latest.Hardware.GPUs[0].Name = "changed"
	if sampler.Latest().Hardware.GPUs[0].Name != "GPU" { t.Fatal("sampler latest must be copied") }
	cancel()

	used := int64(20)
	samples := []telemetry.Sample{{InstanceID:"one", PID:1, GPUDevices:[]string{"CUDA0"}}, {InstanceID:"two", PID:2, GPUDevices:[]string{"missing"}, VRAMUsedBytes:&used}}
	result := applyHardwareFallback(samples, live.Hardware)
	if result[0].VRAMUsedBytes == nil || *result[0].VRAMUsedBytes != 40 || result[0].GPUUtilizationPct == nil || *result[0].GPUUtilizationPct != util { t.Fatalf("fallback=%+v", result[0]) }
	if result[1].VRAMUsedBytes == nil || *result[1].VRAMUsedBytes != used || result[1].GPUUtilizationPct == nil { t.Fatalf("existing/fallback=%+v", result[1]) }
	if got := applyHardwareFallback(nil, live.Hardware); got != nil { t.Fatalf("nil fallback=%v", got) }

	attached := attachNativeMetrics(context.Background(), result, []supervisor.Runtime{{InstanceID:"one", PID:1, State:supervisor.Loading}}, nil)
	if len(attached) != 2 || attached[0].LlamaMetrics != nil { t.Fatalf("attached=%+v", attached) }
}

func TestAttachNativeMetricsForReadyRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" { http.NotFound(w, r); return }
		_, _ = w.Write([]byte("llamacpp:tokens_predicted_total 42\nllamacpp:predicted_tokens_seconds 21\n"))
	}))
	defer server.Close()

	samples := []telemetry.Sample{
		{InstanceID:"ready", PID:7},
		{InstanceID:"mismatch", PID:8},
		{InstanceID:"missing", PID:9},
	}
	runtimes := []supervisor.Runtime{
		{InstanceID:"ready", PID:7, State:supervisor.Ready},
		{InstanceID:"mismatch", PID:99, State:supervisor.Ready},
	}
	resolved := 0
	attached := attachNativeMetrics(context.Background(), samples, runtimes, func(instanceID string) (string, bool) {
		resolved++
		if instanceID == "ready" { return server.URL, true }
		return "", false
	})
	if len(attached) != 3 { t.Fatalf("attached=%+v", attached) }
	if attached[0].LlamaMetrics == nil || attached[0].LlamaMetrics.PredictedTokensTotal == nil || *attached[0].LlamaMetrics.PredictedTokensTotal != 42 {
		t.Fatalf("ready metrics=%+v", attached[0])
	}
	if attached[1].LlamaMetrics != nil || attached[2].LlamaMetrics != nil { t.Fatalf("unexpected metrics=%+v", attached) }
	if resolved != 1 { t.Fatalf("resolver calls=%d", resolved) }

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusServiceUnavailable) }))
	defer bad.Close()
	attached = attachNativeMetrics(context.Background(), []telemetry.Sample{{InstanceID:"ready", PID:7}}, []supervisor.Runtime{{InstanceID:"ready", PID:7, State:supervisor.Ready}}, func(string) (string, bool) { return bad.URL, true })
	if attached[0].LlamaMetrics != nil { t.Fatalf("failed fetch must not populate metrics: %+v", attached[0]) }
}

func TestSamplerRuntimeStatesAndLifecycleObservation(t *testing.T) {
	service := testService(t)
	modelsDir := t.TempDir()
	modelService := models.New(service.db, modelsDir)
	sup := supervisor.New("unused", "127.0.0.1", 39901, time.Second)
	life := lifecycle.New(modelService, sup)
	sampler := NewSampler(life, service)
	ctx := context.Background()

	sampler.refreshRuntimeStates(ctx, map[string]supervisor.Runtime{"one": {InstanceID:"one", State:supervisor.Ready}})
	states := sampler.RuntimeStates()
	if states["one"] != "READY" { t.Fatalf("states=%v", states) }
	states["one"] = "changed"
	if sampler.RuntimeStates()["one"] != "READY" { t.Fatal("runtime state snapshot must be defensive") }

	now := time.Now().UTC()
	sampler.observeLifecycle(ctx,
		supervisor.Runtime{InstanceID:"one", State:supervisor.Loading, StartedAt:now.Add(-2*time.Second)},
		supervisor.Runtime{InstanceID:"one", State:supervisor.Ready, StartedAt:now.Add(-2*time.Second), ReadyAt:now},
	)
	sampler.observeLifecycle(ctx,
		supervisor.Runtime{InstanceID:"one", State:supervisor.Starting},
		supervisor.Runtime{InstanceID:"one", State:supervisor.Failed, LastError:"boom"},
	)
	counters, err := service.Counters(ctx)
	if err != nil { t.Fatal(err) }
	foundLoad, foundFailed := false, false
	for _, counter := range counters {
		if counter.InstanceID == "one" && counter.Metric == "load_total" { foundLoad = counter.Value == 1 }
		if counter.InstanceID == "one" && counter.Metric == "failed_start_total" { foundFailed = counter.Value == 1 }
	}
	if !foundLoad || !foundFailed { t.Fatalf("counters=%+v", counters) }
	logs := strings.Join(life.Logs("one"), "\n")
	if !strings.Contains(logs, "[manager] worker ready") || !strings.Contains(logs, "[manager] worker failed to start: boom") { t.Fatalf("logs=%q", logs) }

	before := len(counters)
	sampler.observeLifecycle(ctx, supervisor.Runtime{InstanceID:"one", State:supervisor.Ready}, supervisor.Runtime{InstanceID:"one", State:supervisor.Ready})
	after, err := service.Counters(ctx)
	if err != nil || len(after) != before { t.Fatalf("same-state observation changed counters: before=%d after=%d err=%v", before, len(after), err) }
}

func TestSamplerRunPublishesManagerOwnedHardware(t *testing.T) {
	service := testService(t)
	modelsDir := t.TempDir()
	if err := os.MkdirAll(modelsDir, 0o755); err != nil { t.Fatal(err) }
	modelService := models.New(service.db, modelsDir)
	sup := supervisor.New("unused", "127.0.0.1", 39900, time.Second)
	life := lifecycle.New(modelService, sup)
	sampler := NewSampler(life, service)
	sampler.interval = 20*time.Millisecond
	sampler.persist = time.Hour
	_, events, unsubscribe := sampler.Subscribe()
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func(){ sampler.Run(ctx); close(done) }()
	select {
	case sample := <-events:
		if sample.Type != "observability" || sample.CollectedAt.IsZero() { t.Fatalf("sample=%+v", sample) }
		if service.LatestHardware().Hardware.CollectedAt.IsZero() { t.Fatal("service latest hardware not updated") }
	case <-time.After(3*time.Second):
		cancel(); t.Fatal("sampler did not publish")
	}
	cancel()
	select { case <-done: case <-time.After(time.Second): t.Fatal("sampler did not stop") }
}
