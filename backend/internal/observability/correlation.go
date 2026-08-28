package observability

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
)

// CorrelatedRequestRecord exposes the stable manager request ID together with
// the persisted observability record. TokensPerSecond on RequestRecord remains
// the generation throughput for backwards compatibility.
type CorrelatedRequestRecord struct {
	RequestID string `json:"request_id"`
	RequestRecord
	PromptTokensPerSecond     *float64 `json:"prompt_tokens_per_second,omitempty"`
	GenerationTokensPerSecond *float64 `json:"generation_tokens_per_second,omitempty"`
}

func (s *Service) EnsureCorrelationSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS inference_request_correlations (
		request_id TEXT PRIMARY KEY,
		inference_request_id INTEGER NOT NULL UNIQUE REFERENCES inference_requests(id) ON DELETE CASCADE,
		prompt_tokens_per_second REAL
	)`)
	return err
}

// RecordCorrelatedRequest persists the normal inference request and its stable
// external correlation ID in one transaction so clients never receive an ID
// that points at a different request row.
func (s *Service) RecordCorrelatedRequest(ctx context.Context, requestID string, promptTokensPerSecond *float64, record RequestRecord) error {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("request_id is required")
	}
	if strings.TrimSpace(record.InstanceID) == "" || strings.TrimSpace(record.Endpoint) == "" {
		return fmt.Errorf("instance_id and endpoint are required")
	}
	if record.Result == "" {
		if record.StatusCode >= 200 && record.StatusCode < 400 {
			record.Result = "success"
		} else {
			record.Result = "error"
		}
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}

	var keyID, keyName, keyPrefix any
	if record.APIKey != nil {
		keyID, keyName, keyPrefix = record.APIKey.ID, record.APIKey.Name, record.APIKey.Prefix
	}
	var ttft, tps, promptTPS, requestBody, responseBody any
	if record.TTFTMS != nil {
		ttft = *record.TTFTMS
	}
	if record.TokensPerSecond != nil {
		tps = *record.TokensPerSecond
	}
	if promptTokensPerSecond != nil {
		promptTPS = *promptTokensPerSecond
	}
	if record.RequestBody != nil {
		requestBody = *record.RequestBody
	}
	if record.ResponseBody != nil {
		responseBody = *record.ResponseBody
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `INSERT INTO inference_requests(
		started_at,finished_at,instance_id,endpoint,api_key_id,api_key_name,api_key_prefix,streaming,status_code,result,
		duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,request_body,response_body
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.StartedAt, record.FinishedAt, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, boolInt(record.Streaming), record.StatusCode, record.Result,
		record.DurationMS, ttft, record.PromptTokens, record.GeneratedTokens, record.TotalTokens, tps, record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), record.Error, requestBody, responseBody)
	if err != nil {
		return err
	}
	rowID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_correlations(request_id,inference_request_id,prompt_tokens_per_second) VALUES(?,?,?)`, requestID, rowID, promptTPS); err != nil {
		return err
	}
	if err := addCounter(ctx, tx, Counter{Metric: "gateway_requests_total", InstanceID: record.InstanceID, Endpoint: record.Endpoint, StatusCode: record.StatusCode, Result: record.Result, Streaming: record.Streaming, Value: 1}); err != nil {
		return err
	}
	for metric, value := range map[string]int64{
		"prompt_tokens_total":    record.PromptTokens,
		"generated_tokens_total": record.GeneratedTokens,
		"tokens_total":           record.TotalTokens,
	} {
		if value > 0 {
			if err := addCounter(ctx, tx, Counter{Metric: metric, InstanceID: record.InstanceID, Endpoint: record.Endpoint, Streaming: record.Streaming, Value: float64(value)}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Service) GetRequestByRequestID(ctx context.Context, requestID string) (CorrelatedRequestRecord, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return CorrelatedRequestRecord{}, fmt.Errorf("request_id is required")
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return CorrelatedRequestRecord{}, err
	}
	var rowID int64
	var promptTPS sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, `SELECT inference_request_id,prompt_tokens_per_second FROM inference_request_correlations WHERE request_id=?`, requestID).Scan(&rowID, &promptTPS); err != nil {
		return CorrelatedRequestRecord{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT id,started_at,finished_at,instance_id,endpoint,api_key_id,api_key_name,api_key_prefix,streaming,status_code,result,duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,request_body,response_body FROM inference_requests WHERE id=?`, rowID)
	record, err := scanRequest(row)
	if err != nil {
		return CorrelatedRequestRecord{}, err
	}
	out := CorrelatedRequestRecord{RequestID: requestID, RequestRecord: record, GenerationTokensPerSecond: record.TokensPerSecond}
	if promptTPS.Valid {
		value := promptTPS.Float64
		out.PromptTokensPerSecond = &value
	}
	return out, nil
}

func NewCorrelatedRequestHandler(service *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requestID := strings.TrimSpace(r.PathValue("request_id"))
		if requestID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id is required"})
			return
		}
		record, err := service.GetRequestByRequestID(r.Context(), requestID)
		if err != nil {
			if err == sql.ErrNoRows {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, record)
	})
}
