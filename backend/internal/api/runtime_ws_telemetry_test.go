package api

import (
	"encoding/json"
	"testing"
	"time"

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
	event := runtimeTelemetryEvent{Type: "runtime_telemetry", Telemetry: []telemetry.Sample{{
		InstanceID: "ready", PID: 42, GPUDevices: []string{"CUDA0"},
		GPUs:          []telemetry.GPUUsage{{DeviceID: "CUDA0", VRAMUsedBytes: &vram, UtilizationPct: &gpuUtil}},
		VRAMUsedBytes: &vram, GPUUtilizationPct: &gpuUtil, CollectedAt: time.Unix(1, 0).UTC(),
	}}}
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
}
