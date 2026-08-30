package lifecycle

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

const (
	ObservabilityAutoload    = "autoload"
	ObservabilityLoad        = "load"
	ObservabilityFailedStart = "failed_start"
	ObservabilityEviction    = "eviction"
	ObservabilityIdleUnload  = "idle_unload"
)

type requestCorrelationContextKey struct{}

// WithRequestCorrelation carries the manager request ID through lifecycle work
// so request-triggered resource changes can be attributed to the request that
// caused them without exposing the value to workers.
func WithRequestCorrelation(ctx context.Context, requestID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestCorrelationContextKey{}, requestID)
}

func RequestCorrelationFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestCorrelationContextKey{}).(string)
	return strings.TrimSpace(requestID)
}

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
