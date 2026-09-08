package observability

import (
	"context"
	"database/sql"
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
	return s.stageInferenceTurnStats(ctx, requestID, stats)
}

// SaveInferenceTurnStats preserves the existing post-correlation API used by
// tests and management code. A finalized request is promoted immediately; a
// pending request is promoted by its finalization trigger.
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
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM inference_request_correlations WHERE request_id=?`, requestID).Scan(&exists); err != nil {
		return err
	}
	return s.stageInferenceTurnStats(ctx, requestID, stats)
}

func (s *Service) stageInferenceTurnStats(ctx context.Context, requestID string, stats InferenceTurnStats) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO inference_request_timing_staging(
		request_id,prompt_n,prompt_ms,prompt_per_second,prompt_per_token_ms,
		predicted_n,predicted_ms,predicted_per_second,predicted_per_token_ms,
		cache_n,draft_n,draft_n_accepted,finish_reason,tool_call_count
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(request_id) DO UPDATE SET
		prompt_n=excluded.prompt_n,prompt_ms=excluded.prompt_ms,prompt_per_second=excluded.prompt_per_second,prompt_per_token_ms=excluded.prompt_per_token_ms,
		predicted_n=excluded.predicted_n,predicted_ms=excluded.predicted_ms,predicted_per_second=excluded.predicted_per_second,predicted_per_token_ms=excluded.predicted_per_token_ms,
		cache_n=excluded.cache_n,draft_n=excluded.draft_n,draft_n_accepted=excluded.draft_n_accepted,finish_reason=excluded.finish_reason,tool_call_count=excluded.tool_call_count`,
		requestID,
		nullableValue(stats.PromptN), nullableValue(stats.PromptMS), nullableValue(stats.PromptPerSecond), nullableValue(stats.PromptPerTokenMS),
		nullableValue(stats.PredictedN), nullableValue(stats.PredictedMS), nullableValue(stats.PredictedPerSecond), nullableValue(stats.PredictedPerTokenMS),
		nullableValue(stats.CacheN), nullableValue(stats.DraftN), nullableValue(stats.DraftNAccepted), nullableValue(stats.FinishReason), nullableValue(stats.ToolCallCount),
	)
	return err
}

func (s *Service) inferenceTurnStats(ctx context.Context, requestID string) (*InferenceTurnStats, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		prompt_n,prompt_ms,prompt_per_second,prompt_per_token_ms,
		predicted_n,predicted_ms,predicted_per_second,predicted_per_token_ms,
		cache_n,draft_n,draft_n_accepted,finish_reason,tool_call_count
		FROM inference_request_timings WHERE request_id=?`, requestID)
	var promptN, predictedN, cacheN, draftN, draftNAccepted, toolCallCount sql.NullInt64
	var promptMS, promptPerSecond, promptPerTokenMS, predictedMS, predictedPerSecond, predictedPerTokenMS sql.NullFloat64
	var finishReason sql.NullString
	if err := row.Scan(
		&promptN, &promptMS, &promptPerSecond, &promptPerTokenMS,
		&predictedN, &predictedMS, &predictedPerSecond, &predictedPerTokenMS,
		&cacheN, &draftN, &draftNAccepted, &finishReason, &toolCallCount,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	stats := &InferenceTurnStats{
		PromptN:             nullInt64Ptr(promptN),
		PromptMS:            nullFloat64Ptr(promptMS),
		PromptPerSecond:     nullFloat64Ptr(promptPerSecond),
		PromptPerTokenMS:    nullFloat64Ptr(promptPerTokenMS),
		PredictedN:          nullInt64Ptr(predictedN),
		PredictedMS:         nullFloat64Ptr(predictedMS),
		PredictedPerSecond:  nullFloat64Ptr(predictedPerSecond),
		PredictedPerTokenMS: nullFloat64Ptr(predictedPerTokenMS),
		CacheN:              nullInt64Ptr(cacheN),
		DraftN:              nullInt64Ptr(draftN),
		DraftNAccepted:      nullInt64Ptr(draftNAccepted),
		FinishReason:        nullStringPtr(finishReason),
		ToolCallCount:       nullInt64Ptr(toolCallCount),
	}
	return stats, nil
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}

func nullFloat64Ptr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	v := value.Float64
	return &v
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}
