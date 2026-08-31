package observability

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/telemetry"
)

type HardwareOverview struct {
	Hardware  hardware.Snapshot `json:"hardware"`
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
	if collectedAt.IsZero() { collectedAt = time.Now().UTC() }
	timestamp := collectedAt.UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	insert := func(metric, deviceID, instanceID string, value float64) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO hardware_metric_samples(collected_at,metric,device_id,instance_id,value) VALUES(?,?,?,?,?)`, timestamp, metric, deviceID, instanceID, value)
		return err
	}

	if snapshot.RAMTotalBytes > 0 {
		if err := insert("ram_total_bytes", "", "", float64(snapshot.RAMTotalBytes)); err != nil { return err }
		used := snapshot.RAMTotalBytes - snapshot.RAMAvailableBytes
		if used < 0 { used = 0 }
		if err := insert("ram_used_bytes", "", "", float64(used)); err != nil { return err }
	}
	var totalVRAM, usedVRAM int64
	var utilization float64
	for _, gpu := range snapshot.GPUs {
		totalVRAM += gpu.TotalBytes
		usedVRAM += gpu.UsedBytes
		utilization += gpu.UtilizationPct
		if err := insert("vram_total_bytes", gpu.ID, "", float64(gpu.TotalBytes)); err != nil { return err }
		if err := insert("vram_used_bytes", gpu.ID, "", float64(gpu.UsedBytes)); err != nil { return err }
		if err := insert("gpu_utilization_pct", gpu.ID, "", gpu.UtilizationPct); err != nil { return err }
	}
	if len(snapshot.GPUs) > 0 {
		if err := insert("vram_total_bytes", "", "", float64(totalVRAM)); err != nil { return err }
		if err := insert("vram_used_bytes", "", "", float64(usedVRAM)); err != nil { return err }
		if err := insert("gpu_utilization_pct", "", "", utilization/float64(len(snapshot.GPUs))); err != nil { return err }
	}
	for _, sample := range samples {
		if sample.VRAMUsedBytes != nil {
			if err := insert("instance_vram_used_bytes", "", sample.InstanceID, float64(*sample.VRAMUsedBytes)); err != nil { return err }
		}
		if sample.CPUPercent != nil {
			if err := insert("instance_cpu_percent", "", sample.InstanceID, *sample.CPUPercent); err != nil { return err }
		}
		if sample.MemoryUsedBytes != nil {
			if err := insert("instance_memory_used_bytes", "", sample.InstanceID, float64(*sample.MemoryUsedBytes)); err != nil { return err }
		}
		for _, gpu := range sample.GPUs {
			if gpu.VRAMUsedBytes != nil {
				if err := insert("instance_vram_used_bytes", gpu.DeviceID, sample.InstanceID, float64(*gpu.VRAMUsedBytes)); err != nil { return err }
			}
		}
	}
	return tx.Commit()
}

func (s *Service) HardwareTimeseries(ctx context.Context, metric string, sinceMS int64, bucketSeconds int, deviceID, instanceID string) ([]HardwareSeriesPoint, error) {
	allowed := map[string]bool{
		"ram_total_bytes": true, "ram_used_bytes": true, "vram_total_bytes": true, "vram_used_bytes": true,
		"gpu_utilization_pct": true, "instance_vram_used_bytes": true, "instance_cpu_percent": true, "instance_memory_used_bytes": true,
	}
	if !allowed[metric] { return nil, fmt.Errorf("unsupported hardware metric %q", metric) }
	if sinceMS <= 0 { sinceMS = time.Now().Add(-time.Hour).UnixMilli() }
	if bucketSeconds <= 0 { bucketSeconds = 60 }
	if bucketSeconds > 24*3600 { bucketSeconds = 24*3600 }
	bucketMS := int64(bucketSeconds) * 1000
	query := `SELECT (collected_at / ?) * ? AS bucket,device_id,instance_id,AVG(value) FROM hardware_metric_samples WHERE metric=? AND collected_at>=?`
	args := []any{bucketMS, bucketMS, metric, sinceMS}
	if deviceID != "" { query += " AND device_id=?"; args = append(args, deviceID) }
	if instanceID != "" { query += " AND instance_id=?"; args = append(args, instanceID) }
	if deviceID == "" && instanceID == "" { query += " AND device_id='' AND instance_id=''" }
	query += " GROUP BY bucket,device_id,instance_id ORDER BY bucket,device_id,instance_id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []HardwareSeriesPoint
	for rows.Next() {
		var point HardwareSeriesPoint
		var value sql.NullFloat64
		if err := rows.Scan(&point.Timestamp, &point.DeviceID, &point.InstanceID, &value); err != nil { return nil, err }
		if value.Valid { point.Value = value.Float64 }
		out = append(out, point)
	}
	return out, rows.Err()
}

func (s *Service) LifecycleSummary(ctx context.Context, sinceMS int64) (LifecycleSummary, error) {
	if sinceMS < 0 { sinceMS = 0 }
	rows, err := s.db.QueryContext(ctx, `SELECT metric,COALESCE(SUM(value),0) FROM observability_counters WHERE metric IN ('load_total','eviction_total','idle_unload_total') GROUP BY metric`)
	if err != nil { return LifecycleSummary{}, err }
	defer rows.Close()
	var summary LifecycleSummary
	for rows.Next() {
		var metric string
		var value float64
		if err := rows.Scan(&metric, &value); err != nil { return LifecycleSummary{}, err }
		switch strings.TrimSpace(metric) {
		case "load_total": summary.Loads = int64(value)
		case "eviction_total": summary.Evictions = int64(value)
		case "idle_unload_total": summary.IdleUnloads = int64(value)
		}
	}
	if err := rows.Err(); err != nil { return LifecycleSummary{}, err }
	// Cold-start KPIs are request-attributed so the dashboard window applies:
	// load → idle-unload → load again is two autoloads, and the same start is
	// not counted once from lifecycle events and again from the request row.
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN finished_at>0 AND result<>'success' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN finished_at>0 THEN load_duration_ms ELSE 0 END),0)
		FROM inference_requests WHERE autoloaded=1 AND started_at>=?`, sinceMS).
		Scan(&summary.Autoloads, &summary.FailedStarts, &summary.LoadMS); err != nil {
		return LifecycleSummary{}, err
	}
	return summary, nil
}

func (s *Service) PruneHardware(ctx context.Context, retentionDays int) error {
	if retentionDays <= 0 { retentionDays = DefaultRetentionDays }
	cutoff := time.Now().Add(-time.Duration(retentionDays)*24*time.Hour).UnixMilli()
	_, err := s.db.ExecContext(ctx, `DELETE FROM hardware_metric_samples WHERE collected_at<?`, cutoff)
	return err
}

func (s *Service) RunHardwareRetention(ctx context.Context, retentionDays func(context.Context) int) {
	prune := func() {
		days := DefaultRetentionDays
		if retentionDays != nil {
			if value := retentionDays(ctx); value > 0 { days = value }
		}
		_ = s.PruneHardware(ctx, days)
	}
	prune()
	ticker := time.NewTicker(6*time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done(): return
		case <-ticker.C: prune()
		}
	}
}
