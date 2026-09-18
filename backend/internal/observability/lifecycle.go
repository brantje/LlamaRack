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
	switch event {
	case LifecycleAutoload:
		return s.recordPlaygroundLifecycleEvent(ctx, event, instanceID)
	case LifecycleLoad, LifecycleFailedStart, LifecycleEviction, LifecycleIdleUnload:
	default:
		return fmt.Errorf("unsupported lifecycle event %q", event)
	}
	if err := s.store.RecordLifecycleCounters(ctx, event, instanceID, float64(duration.Microseconds())/1000); err != nil {
		return err
	}
	return s.recordPlaygroundLifecycleEvent(ctx, event, instanceID)
}
