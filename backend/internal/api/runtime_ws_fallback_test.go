package api

import (
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/telemetry"
)

func TestApplyGlobalTelemetryFallbackFillsOnlyMissingMetrics(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	processUtil := 17.0
	processVRAM := int64(3 * gib)
	cuda0VRAM := int64(2 * gib)
	cuda1Util := 9.0
	samples := []telemetry.Sample{
		{InstanceID: "docker-unmatched", PID: 42, GPUDevices: []string{}, GPUs: []telemetry.GPUUsage{}},
		{InstanceID: "fully-attributed", PID: 43, GPUDevices: []string{"CUDA0"}, GPUs: []telemetry.GPUUsage{{DeviceID: "CUDA0", VRAMUsedBytes: &processVRAM, UtilizationPct: &processUtil}}, GPUUtilizationPct: &processUtil, VRAMUsedBytes: &processVRAM},
		{InstanceID: "placement-and-vram-only", PID: 44, GPUDevices: []string{"CUDA0"}, GPUs: []telemetry.GPUUsage{{DeviceID: "CUDA0", VRAMUsedBytes: &cuda0VRAM}}, VRAMUsedBytes: &cuda0VRAM},
		{InstanceID: "placement-and-util-only", PID: 45, GPUDevices: []string{"CUDA1"}, GPUs: []telemetry.GPUUsage{{DeviceID: "CUDA1", UtilizationPct: &cuda1Util}}, GPUUtilizationPct: &cuda1Util},
	}
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", UtilizationPct: 80, UsedBytes: 2 * gib},
		{ID: "CUDA1", UtilizationPct: 20, UsedBytes: 4 * gib},
	}}

	got := applyGlobalTelemetryFallback(samples, snapshot)
	if got[0].GPUUtilizationPct == nil || *got[0].GPUUtilizationPct != 50 {
		t.Fatalf("unattributed global utilization=%v", got[0].GPUUtilizationPct)
	}
	if got[0].VRAMUsedBytes == nil || *got[0].VRAMUsedBytes != 6*gib {
		t.Fatalf("unattributed global VRAM=%v", got[0].VRAMUsedBytes)
	}
	if len(got[0].GPUDevices) != 0 || len(got[0].GPUs) != 0 {
		t.Fatalf("fallback must not invent placement: %+v", got[0])
	}

	if got[1].GPUUtilizationPct == nil || *got[1].GPUUtilizationPct != 17 || got[1].VRAMUsedBytes == nil || *got[1].VRAMUsedBytes != 3*gib {
		t.Fatalf("fully process-scoped telemetry was overwritten: %+v", got[1])
	}

	if got[2].GPUUtilizationPct == nil || *got[2].GPUUtilizationPct != 80 {
		t.Fatalf("assigned-device utilization fallback=%v", got[2].GPUUtilizationPct)
	}
	if got[2].VRAMUsedBytes == nil || *got[2].VRAMUsedBytes != 2*gib {
		t.Fatalf("process VRAM must remain attributed: %+v", got[2])
	}

	if got[3].GPUUtilizationPct == nil || *got[3].GPUUtilizationPct != 9 {
		t.Fatalf("process utilization must remain attributed: %+v", got[3])
	}
	if got[3].VRAMUsedBytes == nil || *got[3].VRAMUsedBytes != 4*gib {
		t.Fatalf("assigned-device VRAM fallback=%v", got[3].VRAMUsedBytes)
	}
}

func TestApplyGlobalTelemetryFallbackNeedsDetectedGPUs(t *testing.T) {
	samples := []telemetry.Sample{{InstanceID: "one", PID: 42}}
	got := applyGlobalTelemetryFallback(samples, hardware.Snapshot{})
	if got[0].GPUUtilizationPct != nil || got[0].VRAMUsedBytes != nil {
		t.Fatalf("unexpected fallback without GPUs: %+v", got[0])
	}
}
