package api

import (
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/telemetry"
)

func TestApplyGlobalTelemetryFallbackOnlyWhenPlacementIsUnattributed(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	processUtil := 17.0
	processVRAM := int64(3 * gib)
	samples := []telemetry.Sample{
		{InstanceID: "docker-unmatched", PID: 42, GPUDevices: []string{}, GPUs: []telemetry.GPUUsage{}},
		{InstanceID: "attributed", PID: 43, GPUDevices: []string{"CUDA0"}, GPUs: []telemetry.GPUUsage{{DeviceID: "CUDA0"}}, GPUUtilizationPct: &processUtil, VRAMUsedBytes: &processVRAM},
	}
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", UtilizationPct: 80, UsedBytes: 2 * gib},
		{ID: "CUDA1", UtilizationPct: 20, UsedBytes: 4 * gib},
	}}

	got := applyGlobalTelemetryFallback(samples, snapshot)
	if got[0].GPUUtilizationPct == nil || *got[0].GPUUtilizationPct != 50 {
		t.Fatalf("global utilization=%v", got[0].GPUUtilizationPct)
	}
	if got[0].VRAMUsedBytes == nil || *got[0].VRAMUsedBytes != 6*gib {
		t.Fatalf("global VRAM=%v", got[0].VRAMUsedBytes)
	}
	if len(got[0].GPUDevices) != 0 || len(got[0].GPUs) != 0 {
		t.Fatalf("fallback must not invent placement: %+v", got[0])
	}
	if got[1].GPUUtilizationPct == nil || *got[1].GPUUtilizationPct != 17 || got[1].VRAMUsedBytes == nil || *got[1].VRAMUsedBytes != 3*gib {
		t.Fatalf("process-scoped telemetry was overwritten: %+v", got[1])
	}
}

func TestApplyGlobalTelemetryFallbackNeedsDetectedGPUs(t *testing.T) {
	samples := []telemetry.Sample{{InstanceID: "one", PID: 42}}
	got := applyGlobalTelemetryFallback(samples, hardware.Snapshot{})
	if got[0].GPUUtilizationPct != nil || got[0].VRAMUsedBytes != nil {
		t.Fatalf("unexpected fallback without GPUs: %+v", got[0])
	}
}
