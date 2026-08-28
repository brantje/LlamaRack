package observability

import (
	"context"
	"os"
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
