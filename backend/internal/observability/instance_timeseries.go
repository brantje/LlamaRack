package observability

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RequestTimeseries returns bounded, server-bucketed gateway history. The
// optional instanceID scopes every request-backed series to one durable
// Instance without requiring the frontend to enumerate retained requests.
func (s *Service) RequestTimeseries(ctx context.Context, metric string, sinceMS int64, bucketSeconds int, instanceID string) ([]SeriesPoint, error) {
	if sinceMS <= 0 {
		sinceMS = s.now().Add(-time.Hour).UnixMilli()
	}
	if bucketSeconds <= 0 {
		bucketSeconds = 60
	}
	if bucketSeconds > 24*3600 {
		bucketSeconds = 24 * 3600
	}
	bucketMS := int64(bucketSeconds) * 1000
	metric = strings.TrimSpace(metric)
	if metric == "" {
		metric = "requests"
	}

	if metric == "latency_p50" || metric == "latency_p95" {
		query := `SELECT started_at,duration_ms FROM inference_requests WHERE started_at>=? AND finished_at>0`
		args := []any{sinceMS}
		if instanceID != "" {
			query += " AND instance_id=?"
			args = append(args, instanceID)
		}
		query += " ORDER BY started_at"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		buckets := map[int64][]float64{}
		for rows.Next() {
			var startedAt int64
			var duration float64
			if err := rows.Scan(&startedAt, &duration); err != nil {
				return nil, err
			}
			bucket := (startedAt / bucketMS) * bucketMS
			buckets[bucket] = append(buckets[bucket], duration)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		keys := make([]int64, 0, len(buckets))
		for bucket := range buckets {
			keys = append(keys, bucket)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		out := make([]SeriesPoint, 0, len(keys))
		for _, bucket := range keys {
			values := percentiles(buckets[bucket])
			selected := values.P50
			if metric == "latency_p95" {
				selected = values.P95
			}
			if selected != nil {
				out = append(out, SeriesPoint{Timestamp: bucket, Value: *selected})
			}
		}
		return out, nil
	}

	if metric == "instance_context_tokens_max" {
		query := `SELECT (collected_at / ?) * ? AS bucket,MAX(value) FROM hardware_metric_samples WHERE metric='instance_context_tokens_max' AND collected_at>=?`
		args := []any{bucketMS, bucketMS, sinceMS}
		if instanceID != "" {
			query += " AND instance_id=?"
			args = append(args, instanceID)
		}
		query += " GROUP BY bucket ORDER BY bucket"
		return scanSeriesRows(s.db.QueryContext(ctx, query, args...))
	}

	expression := ""
	switch metric {
	case "requests":
		expression = "COUNT(*)"
	case "latency":
		expression = "AVG(duration_ms)"
	case "ttft":
		expression = "AVG(ttft_ms)"
	case "tokens":
		expression = "COALESCE(SUM(total_tokens),0)"
	case "prompt_tokens":
		expression = "COALESCE(SUM(prompt_tokens),0)"
	case "generated_tokens":
		expression = "COALESCE(SUM(generated_tokens),0)"
	default:
		return nil, fmt.Errorf("unsupported metric %q", metric)
	}
	query := fmt.Sprintf(`SELECT (started_at / ?) * ? AS bucket,%s FROM inference_requests WHERE started_at>=? AND finished_at>0`, expression)
	args := []any{bucketMS, bucketMS, sinceMS}
	if instanceID != "" {
		query += " AND instance_id=?"
		args = append(args, instanceID)
	}
	query += " GROUP BY bucket ORDER BY bucket"
	return scanSeriesRows(s.db.QueryContext(ctx, query, args...))
}

func scanSeriesRows(rows *sql.Rows, err error) ([]SeriesPoint, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeriesPoint
	for rows.Next() {
		var point SeriesPoint
		var value sql.NullFloat64
		if err := rows.Scan(&point.Timestamp, &value); err != nil {
			return nil, err
		}
		if value.Valid {
			point.Value = value.Float64
		}
		out = append(out, point)
	}
	return out, rows.Err()
}

// RecordContextMetrics persists only the derived context high-watermark needed
// by the Instance detail chart. Full llama.cpp metric snapshots remain live-only.
func (s *Service) RecordContextMetrics(ctx context.Context, collectedAt time.Time, samples []RuntimeTelemetrySample) error {
	if collectedAt.IsZero() {
		collectedAt = time.Now().UTC()
	}
	timestamp := collectedAt.UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, sample := range samples {
		if sample.InstanceID == "" || sample.LlamaMetrics == nil || sample.LlamaMetrics.ContextTokensMax == nil {
			continue
		}
		value := *sample.LlamaMetrics.ContextTokensMax
		if value < 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO hardware_metric_samples(collected_at,metric,device_id,instance_id,value) VALUES(?,?,?,?,?)`, timestamp, "instance_context_tokens_max", "", sample.InstanceID, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
