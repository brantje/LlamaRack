package observability

import (
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/systemlog"
	"github.com/brantje/llamacpp-manager/backend/internal/telemetry"
)

func TestHardwareFallbackEmitsDeviceWideDiagnostic(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()

	used := int64(7)
	util := 12.0
	samples := []telemetry.Sample{
		{InstanceID: "embeddings", GPUDevices: []string{"CUDA1"}},
		{InstanceID: "known", GPUDevices: []string{"CUDA0"}, GPUUtilizationPct: &util, VRAMUsedBytes: &used},
	}
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", UsedBytes: 20, UtilizationPct: 25},
		{ID: "CUDA1", UsedBytes: 40, UtilizationPct: 75},
	}}

	got := applyHardwareFallback(samples, snapshot)
	if got[0].GPUUtilizationPct == nil || *got[0].GPUUtilizationPct != 75 || got[0].VRAMUsedBytes == nil || *got[0].VRAMUsedBytes != 40 {
		t.Fatalf("fallback=%+v", got[0])
	}
	if got[1].GPUUtilizationPct != &util && *got[1].GPUUtilizationPct != util {
		t.Fatalf("known utilization changed: %+v", got[1])
	}
	logs := systemlog.Default.Snapshot(10)
	if len(logs) != 1 || logs[0].Level != systemlog.Debug || logs[0].Source != "telemetry" || logs[0].Message != "GPU util for embeddings unavailable, using CUDA1 device-wide (global fallback)" {
		t.Fatalf("diagnostics=%+v", logs)
	}
}

func TestHardwareFallbackUsesAllDevicesWhenAssignmentUnknown(t *testing.T) {
	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	samples := []telemetry.Sample{{InstanceID: "unknown", GPUDevices: []string{"missing"}}}
	snapshot := hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", UsedBytes: 10, UtilizationPct: 20},
		{ID: "CUDA1", UsedBytes: 30, UtilizationPct: 60},
	}}
	got := applyHardwareFallback(samples, snapshot)
	if got[0].GPUUtilizationPct == nil || *got[0].GPUUtilizationPct != 40 || got[0].VRAMUsedBytes == nil || *got[0].VRAMUsedBytes != 40 {
		t.Fatalf("fallback=%+v", got[0])
	}
	logs := systemlog.Default.Snapshot(10)
	if len(logs) != 1 || logs[0].Message != "GPU util for unknown unavailable, using CUDA0,CUDA1 device-wide (global fallback)" {
		t.Fatalf("diagnostics=%+v", logs)
	}
}
