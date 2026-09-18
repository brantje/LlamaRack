package observability

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/telemetry"
)

type HardwareOverview struct {
	Hardware  hardware.Snapshot  `json:"hardware"`
	Telemetry []telemetry.Sample `json:"telemetry"`
}

type HardwareSeriesPoint struct {
	Timestamp  int64   `json:"timestamp"`
	DeviceID   string  `json:"device_id,omitempty"`
	InstanceID string  `json:"instance_id,omitempty"`
	Value      float64 `json:"value"`
}

type LifecycleSummary struct {
	Autoloads    int64   `json:"autoloads"`
	Loads        int64   `json:"loads"`
	FailedStarts int64   `json:"failed_starts"`
	Evictions    int64   `json:"evictions"`
	IdleUnloads  int64   `json:"idle_unloads"`
	LoadMS       float64 `json:"load_duration_ms_total"`
}

var latestHardware sync.Map // map[*Service]HardwareOverview

func cloneTelemetrySamples(samples []telemetry.Sample) []telemetry.Sample {
	out := make([]telemetry.Sample, len(samples))
	for index := range samples {
		out[index] = samples[index]
		out[index].GPUDevices = append([]string(nil), samples[index].GPUDevices...)
		out[index].GPUs = append([]telemetry.GPUUsage(nil), samples[index].GPUs...)
	}
	return out
}

func cloneHardwareOverview(value HardwareOverview) HardwareOverview {
	out := value
	out.Hardware.GPUs = append([]hardware.GPU(nil), value.Hardware.GPUs...)
	out.Hardware.Processes = append([]hardware.GPUProcess(nil), value.Hardware.Processes...)
	out.Telemetry = cloneTelemetrySamples(value.Telemetry)
	return out
}

func (s *Service) SetLatestHardware(snapshot hardware.Snapshot, samples []telemetry.Sample) {
	latestHardware.Store(s, cloneHardwareOverview(HardwareOverview{Hardware: snapshot, Telemetry: samples}))
}

func (s *Service) LatestHardware() HardwareOverview {
	value, ok := latestHardware.Load(s)
	if !ok {
		return HardwareOverview{Hardware: hardware.Snapshot{GPUs: []hardware.GPU{}, Processes: []hardware.GPUProcess{}}, Telemetry: []telemetry.Sample{}}
	}
	return cloneHardwareOverview(value.(HardwareOverview))
}

func (s *Service) RecordHardware(ctx context.Context, snapshot hardware.Snapshot, samples []telemetry.Sample) error {
	s.SetLatestHardware(snapshot, samples)
	collectedAt := snapshot.CollectedAt.UTC()
	if collectedAt.IsZero() {
		collectedAt = time.Now().UTC()
	}
	return s.store.RecordHardware(ctx, snapshot, samples, collectedAt.UnixMilli())
}

func (s *Service) HardwareTimeseries(ctx context.Context, metric string, sinceMS int64, bucketSeconds int, deviceID, instanceID string) ([]HardwareSeriesPoint, error) {
	allowed := map[string]bool{
		"ram_total_bytes": true, "ram_used_bytes": true, "vram_total_bytes": true, "vram_used_bytes": true,
		"gpu_utilization_pct": true, "instance_vram_used_bytes": true, "instance_cpu_percent": true, "instance_memory_used_bytes": true,
	}
	if !allowed[metric] {
		return nil, fmt.Errorf("unsupported hardware metric %q", metric)
	}
	if sinceMS <= 0 {
		sinceMS = time.Now().Add(-time.Hour).UnixMilli()
	}
	if bucketSeconds <= 0 {
		bucketSeconds = 60
	}
	if bucketSeconds > 24*3600 {
		bucketSeconds = 24 * 3600
	}
	return s.store.HardwareTimeseries(ctx, metric, sinceMS, bucketSeconds, deviceID, instanceID)
}

func (s *Service) LifecycleSummary(ctx context.Context, sinceMS int64) (LifecycleSummary, error) {
	if sinceMS < 0 {
		sinceMS = 0
	}
	return s.store.LifecycleSummary(ctx, sinceMS)
}

func (s *Service) PruneHardware(ctx context.Context, retentionDays int) error {
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).UnixMilli()
	return s.store.PruneHardware(ctx, cutoff)
}

func (s *Service) RunHardwareRetention(ctx context.Context, retentionDays func(context.Context) int) {
	prune := func() {
		days := DefaultRetentionDays
		if retentionDays != nil {
			if value := retentionDays(ctx); value > 0 {
				days = value
			}
		}
		_ = s.PruneHardware(ctx, days)
	}
	prune()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}
