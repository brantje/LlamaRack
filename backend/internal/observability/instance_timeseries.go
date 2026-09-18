package observability

import (
	"context"
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
	metric = strings.TrimSpace(metric)
	if metric == "" {
		metric = "requests"
	}
	return s.store.RequestTimeseries(ctx, metric, sinceMS, bucketSeconds, instanceID)
}

// RecordContextMetrics persists only the derived context high-watermark needed
// by the Instance detail chart. Full llama.cpp metric snapshots remain live-only.
func (s *Service) RecordContextMetrics(ctx context.Context, collectedAt time.Time, samples []RuntimeTelemetrySample) error {
	if collectedAt.IsZero() {
		collectedAt = time.Now().UTC()
	}
	return s.store.RecordContextMetrics(ctx, collectedAt.UTC().UnixMilli(), samples)
}
