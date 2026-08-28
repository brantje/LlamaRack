package lifecycle

import (
	"context"
	"log/slog"
	"time"
)

const (
	ObservabilityAutoload    = "autoload"
	ObservabilityLoad        = "load"
	ObservabilityFailedStart = "failed_start"
	ObservabilityEviction    = "eviction"
	ObservabilityIdleUnload  = "idle_unload"
)

type ObservabilityRecorder func(context.Context, string, string, time.Duration) error

func (s *Service) SetObservabilityRecorder(recorder ObservabilityRecorder) {
	s.mu.Lock()
	s.observabilityRecorder = recorder
	s.mu.Unlock()
}

func (s *Service) recordObservabilityEvent(ctx context.Context, event, instanceID string, duration time.Duration) {
	s.mu.Lock()
	recorder := s.observabilityRecorder
	s.mu.Unlock()
	if recorder == nil {
		return
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := recorder(recordCtx, event, instanceID, duration); err != nil {
		slog.Warn("unable to persist lifecycle observability event", "event", event, "instance_id", instanceID, "error", err)
	}
}
