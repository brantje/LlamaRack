package observability

import (
	"context"
	"fmt"
	"strings"
)

// InferenceTurnStats contains per-request llama.cpp telemetry that is useful to
// Playground diagnostics but intentionally does not belong to generic request
// log records. Pointer fields preserve the distinction between an unavailable
// metric and an authoritative zero value.
type InferenceTurnStats struct {
	PromptN             *int64   `json:"prompt_n,omitempty"`
	PromptMS            *float64 `json:"prompt_ms,omitempty"`
	PromptPerSecond     *float64 `json:"prompt_per_second,omitempty"`
	PromptPerTokenMS    *float64 `json:"prompt_per_token_ms,omitempty"`
	PredictedN          *int64   `json:"predicted_n,omitempty"`
	PredictedMS         *float64 `json:"predicted_ms,omitempty"`
	PredictedPerSecond  *float64 `json:"predicted_per_second,omitempty"`
	PredictedPerTokenMS *float64 `json:"predicted_per_token_ms,omitempty"`
	CacheN              *int64   `json:"cache_n,omitempty"`
	DraftN              *int64   `json:"draft_n,omitempty"`
	DraftNAccepted      *int64   `json:"draft_n_accepted,omitempty"`
	FinishReason        *string  `json:"finish_reason,omitempty"`
	ToolCallCount       *int64   `json:"tool_call_count,omitempty"`
}

func (s InferenceTurnStats) Empty() bool {
	return s.PromptN == nil && s.PromptMS == nil && s.PromptPerSecond == nil && s.PromptPerTokenMS == nil &&
		s.PredictedN == nil && s.PredictedMS == nil && s.PredictedPerSecond == nil && s.PredictedPerTokenMS == nil &&
		s.CacheN == nil && s.DraftN == nil && s.DraftNAccepted == nil && s.FinishReason == nil && s.ToolCallCount == nil
}

func nullableValue[T any](value *T) any {
	if value == nil {
		return nil
	}
	return *value
}

// StageInferenceTurnStats records specialist per-request telemetry before the
// correlated request is finalized. Database triggers promote the staged row
// into inference_request_timings in the same transaction that finalizes the
// request (or creates its recovery correlation).
func (s *Service) StageInferenceTurnStats(ctx context.Context, requestID string, stats InferenceTurnStats) error {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("request_id is required")
	}
	if stats.Empty() {
		return nil
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	return s.store.StageInferenceTurnStats(ctx, requestID, stats)
}

func (s *Service) SaveInferenceTurnStats(ctx context.Context, requestID string, stats InferenceTurnStats) error {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("request_id is required")
	}
	if stats.Empty() {
		return nil
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	return s.store.SaveInferenceTurnStats(ctx, requestID, stats)
}

func (s *Service) inferenceTurnStats(ctx context.Context, requestID string) (*InferenceTurnStats, error) {
	return s.store.InferenceTurnStats(ctx, requestID)
}
