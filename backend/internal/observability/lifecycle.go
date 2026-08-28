package observability

import (
	"context"
	"fmt"
	"time"
)

const (
	LifecycleAutoload    = "autoload"
	LifecycleLoad        = "load"
	LifecycleFailedStart = "failed_start"
	LifecycleEviction    = "eviction"
	LifecycleIdleUnload  = "idle_unload"
)

func (s *Service) RecordLifecycle(ctx context.Context, event, instanceID string, duration time.Duration) error {
	metric := ""
	switch event {
	case LifecycleAutoload:
		metric = "autoload_total"
	case LifecycleLoad:
		metric = "load_total"
	case LifecycleFailedStart:
		metric = "failed_start_total"
	case LifecycleEviction:
		metric = "eviction_total"
	case LifecycleIdleUnload:
		metric = "idle_unload_total"
	default:
		return fmt.Errorf("unsupported lifecycle event %q", event)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	defer tx.Rollback()
	if err := addCounter(ctx, tx, Counter{Metric: metric, InstanceID: instanceID, Value: 1}); err != nil { return err }
	if event == LifecycleLoad && duration > 0 {
		if err := addCounter(ctx, tx, Counter{Metric: "load_duration_ms_total", InstanceID: instanceID, Value: float64(duration.Microseconds()) / 1000}); err != nil { return err }
	}
	return tx.Commit()
}
