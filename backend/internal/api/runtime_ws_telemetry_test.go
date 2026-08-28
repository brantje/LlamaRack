package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/observability"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
	"github.com/brantje/llamacpp-manager/backend/internal/telemetry"
)

func TestRuntimeTelemetryHelpersAndEventShape(t *testing.T) {
	current := map[string]supervisor.Runtime{
		"stopped": {InstanceID: "stopped", State: supervisor.Unloaded},
		"ready":   {InstanceID: "ready", State: supervisor.Ready, PID: 42},
	}
	values := runtimeValues(current)
	if len(values) != 2 || !hasRunningRuntime(values) {
		t.Fatalf("runtime values=%+v", values)
	}
	if hasRunningRuntime([]supervisor.Runtime{{InstanceID: "off", State: supervisor.Unloaded}}) {
		t.Fatal("unloaded runtimes must not trigger telemetry collection")
	}

	gpuUtil := 17.0
	vram := int64(2048)
	predicted := 52.9
	shared := []observability.RuntimeTelemetrySample{{
		Sample: telemetry.Sample{
			InstanceID: "ready", PID: 42, GPUDevices: []string{"CUDA0"},
			GPUs:          []telemetry.GPUUsage{{DeviceID: "CUDA0", VRAMUsedBytes: &vram, UtilizationPct: &gpuUtil}},
			VRAMUsedBytes: &vram, GPUUtilizationPct: &gpuUtil, CollectedAt: time.Unix(1, 0).UTC(),
		},
		LlamaMetrics: &telemetry.LlamaMetrics{PredictedTokensPerSecond: &predicted},
	}}
	converted := sharedTelemetry(shared)
	if len(converted) != 1 || converted[0].InstanceID != "ready" || converted[0].LlamaMetrics == nil || converted[0].LlamaMetrics.PredictedTokensPerSecond == nil {
		t.Fatalf("converted=%+v", converted)
	}
	event := runtimeTelemetryEvent{Type: "runtime_telemetry", Telemetry: converted}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != "runtime_telemetry" {
		t.Fatalf("event=%s", payload)
	}
	items, ok := decoded["telemetry"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("event=%s", payload)
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["instance_id"] != "ready" {
		t.Fatalf("event=%s", payload)
	}
	metrics, ok := item["llama_metrics"].(map[string]any)
	if !ok || metrics["predicted_tokens_per_second"] != predicted {
		t.Fatalf("event=%s", payload)
	}
}
