package observability

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CorrelatedRequestRecord is the request detail DTO. Full-mode bodies are
// deliberately exposed here only; RequestRecord itself never serializes them.
type CorrelatedRequestRecord struct {
	RequestRecord
	RequestBody  *string `json:"request_body,omitempty"`
	ResponseBody *string `json:"response_body,omitempty"`
}

func (s *Service) EnsureCorrelationSchema(ctx context.Context) error {
	s.correlationMu.Lock()
	defer s.correlationMu.Unlock()
	if s.correlationReady {
		return nil
	}
	if s.db == nil {
		return fmt.Errorf("database unavailable")
	}
	s.correlationReady = true
	return nil
}

func requestValues(record RequestRecord) (keyID, keyName, keyPrefix, ttft, tps, requestBody, responseBody any) {
	if record.APIKey != nil {
		keyID, keyName, keyPrefix = record.APIKey.ID, record.APIKey.Name, record.APIKey.Prefix
	}
	if record.TTFTMS != nil {
		ttft = *record.TTFTMS
	}
	if record.TokensPerSecond != nil {
		tps = *record.TokensPerSecond
	}
	if record.RequestBody != nil {
		requestBody = *record.RequestBody
	}
	if record.ResponseBody != nil {
		responseBody = *record.ResponseBody
	}
	return
}

// BeginCorrelatedRequest creates the durable request row before authentication,
// validation, Instance resolution or worker acquisition can fail.
func (s *Service) BeginCorrelatedRequest(ctx context.Context, requestID string, record RequestRecord) error {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("request_id is required")
	}
	if strings.TrimSpace(record.Endpoint) == "" {
		return fmt.Errorf("endpoint is required")
	}
	if record.StartedAt <= 0 {
		return fmt.Errorf("started_at is required")
	}
	if handled, err := s.bufferBegin(requestID, record); handled {
		return err
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	keyID, keyName, keyPrefix, _, _, requestBody, _ := requestValues(record)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO inference_requests(
		started_at,finished_at,instance_id,endpoint,api_key_id,api_key_name,api_key_prefix,streaming,status_code,result,
		duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,request_body,response_body,
		trace_id,call_type,client_ip,user_agent
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.StartedAt, 0, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, boolInt(record.Streaming), 0, "pending",
		0, nil, 0, 0, 0, nil, 0, 0, 0, "", requestBody, nil,
		record.TraceID, record.CallType, record.ClientIP, record.UserAgent)
	if err != nil {
		return err
	}
	rowID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_correlations(request_id,inference_request_id,prompt_tokens_per_second) VALUES(?,?,NULL)`, requestID, rowID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateCorrelatedRequest persists metadata learned while the request remains in
// flight (safe API-key identity, canonical Instance identity and opt-in body).
func (s *Service) UpdateCorrelatedRequest(ctx context.Context, requestID string, record RequestRecord) error {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("request_id is required")
	}
	if handled, err := s.bufferUpdate(requestID, record); handled {
		return err
	}
	if s.writebackEnabled() {
		return nil
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	keyID, keyName, keyPrefix, _, _, requestBody, _ := requestValues(record)
	result, err := s.db.ExecContext(ctx, `UPDATE inference_requests SET
		instance_id=?,endpoint=?,api_key_id=?,api_key_name=?,api_key_prefix=?,streaming=?,queue_duration_ms=?,load_duration_ms=?,autoloaded=?,request_body=?,
		trace_id=?,call_type=?,client_ip=?,user_agent=?
		WHERE id=(SELECT inference_request_id FROM inference_request_correlations WHERE request_id=?) AND finished_at=0`,
		record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, boolInt(record.Streaming), record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), requestBody,
		record.TraceID, record.CallType, record.ClientIP, record.UserAgent, requestID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func normalizeFinalRecord(record RequestRecord) RequestRecord {
	if record.FinishedAt == 0 {
		record.FinishedAt = time.Now().UnixMilli()
	}
	if record.Result == "" {
		if record.StatusCode >= 200 && record.StatusCode < 400 {
			record.Result = "success"
		} else {
			record.Result = "error"
		}
	}
	return record
}

// FinalizeCorrelatedRequest is idempotent. Only the first transition from a
// pending row to a completed row increments cumulative counters. If the early
// insert failed, it recovers with an atomic final insert.
func (s *Service) FinalizeCorrelatedRequest(ctx context.Context, requestID string, promptTokensPerSecond *float64, record RequestRecord) error {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("request_id is required")
	}
	if strings.TrimSpace(record.Endpoint) == "" {
		return fmt.Errorf("endpoint is required")
	}
	if handled, err := s.bufferFinalize(requestID, promptTokensPerSecond, record); handled {
		return err
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	record = normalizeFinalRecord(record)
	keyID, keyName, keyPrefix, ttft, tps, requestBody, responseBody := requestValues(record)
	var promptTPS any
	if promptTokensPerSecond != nil {
		promptTPS = *promptTokensPerSecond
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE inference_requests SET
		started_at=?,finished_at=?,instance_id=?,endpoint=?,api_key_id=?,api_key_name=?,api_key_prefix=?,streaming=?,status_code=?,result=?,duration_ms=?,ttft_ms=?,
		prompt_tokens=?,generated_tokens=?,total_tokens=?,tokens_per_second=?,queue_duration_ms=?,load_duration_ms=?,autoloaded=?,error=?,request_body=?,response_body=?,
		trace_id=?,call_type=?,client_ip=?,user_agent=?
		WHERE id=(SELECT inference_request_id FROM inference_request_correlations WHERE request_id=?) AND finished_at=0`,
		record.StartedAt, record.FinishedAt, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, boolInt(record.Streaming), record.StatusCode, record.Result, record.DurationMS, ttft,
		record.PromptTokens, record.GeneratedTokens, record.TotalTokens, tps, record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), record.Error, requestBody, responseBody,
		record.TraceID, record.CallType, record.ClientIP, record.UserAgent, requestID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var existing int64
		err := tx.QueryRowContext(ctx, `SELECT inference_request_id FROM inference_request_correlations WHERE request_id=?`, requestID).Scan(&existing)
		if err == nil {
			// Already finalized: the exactly-once operation has completed.
			return tx.Commit()
		}
		if err != sql.ErrNoRows {
			return err
		}
		inserted, err := tx.ExecContext(ctx, `INSERT INTO inference_requests(
			started_at,finished_at,instance_id,endpoint,api_key_id,api_key_name,api_key_prefix,streaming,status_code,result,
			duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,request_body,response_body,
			trace_id,call_type,client_ip,user_agent
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			record.StartedAt, record.FinishedAt, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, boolInt(record.Streaming), record.StatusCode, record.Result,
			record.DurationMS, ttft, record.PromptTokens, record.GeneratedTokens, record.TotalTokens, tps, record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), record.Error, requestBody, responseBody,
			record.TraceID, record.CallType, record.ClientIP, record.UserAgent)
		if err != nil {
			return err
		}
		rowID, err := inserted.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_correlations(request_id,inference_request_id,prompt_tokens_per_second) VALUES(?,?,?)`, requestID, rowID, promptTPS); err != nil {
			return err
		}
		if err := addFinalCounters(ctx, tx, record); err != nil {
			return err
		}
		// Insert triggers already account for autoload/load/failure lifecycle counters.
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE inference_request_correlations SET prompt_tokens_per_second=? WHERE request_id=?`, promptTPS, requestID); err != nil {
		return err
	}
	if err := addFinalCounters(ctx, tx, record); err != nil {
		return err
	}
	// Early rows were inserted with autoloaded=0, so the legacy INSERT triggers
	// did not fire. Preserve those cumulative lifecycle metrics at finalization.
	if record.Autoloaded {
		if err := addCounter(ctx, tx, Counter{Metric: "autoload_total", InstanceID: record.InstanceID, Value: 1}); err != nil {
			return err
		}
		if record.LoadDurationMS > 0 {
			if err := addCounter(ctx, tx, Counter{Metric: "load_duration_ms_total", InstanceID: record.InstanceID, Value: record.LoadDurationMS}); err != nil {
				return err
			}
		}
		if record.Result != "success" {
			if err := addCounter(ctx, tx, Counter{Metric: "failed_start_total", InstanceID: record.InstanceID, Value: 1}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// RecordCorrelatedRequest preserves the completion-only API used by existing
// callers/tests while gateway traffic uses the early durable lifecycle above.
func (s *Service) RecordCorrelatedRequest(ctx context.Context, requestID string, promptTokensPerSecond *float64, record RequestRecord) error {
	if strings.TrimSpace(record.InstanceID) == "" || strings.TrimSpace(record.Endpoint) == "" {
		return fmt.Errorf("instance_id and endpoint are required")
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("request_id is required")
	}
	if record.StartedAt <= 0 {
		return fmt.Errorf("started_at is required")
	}
	if handled, err := s.bufferRecordCorrelated(requestID, promptTokensPerSecond, record); handled {
		return err
	}
	if err := s.BeginCorrelatedRequest(ctx, requestID, record); err != nil {
		return err
	}
	return s.FinalizeCorrelatedRequest(ctx, requestID, promptTokensPerSecond, record)
}

func (s *Service) GetRequestByRequestID(ctx context.Context, requestID string) (CorrelatedRequestRecord, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return CorrelatedRequestRecord{}, fmt.Errorf("request_id is required")
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return CorrelatedRequestRecord{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT COALESCE(c.request_id,''),
		r.id,r.trace_id,r.call_type,r.started_at,r.finished_at,r.instance_id,r.endpoint,r.api_key_id,r.api_key_name,r.api_key_prefix,r.client_ip,r.user_agent,
		r.streaming,r.status_code,r.result,r.duration_ms,r.ttft_ms,r.prompt_tokens,r.generated_tokens,r.total_tokens,r.tokens_per_second,
		c.prompt_tokens_per_second,r.queue_duration_ms,r.load_duration_ms,r.autoloaded,r.error,r.request_body,r.response_body
		FROM inference_requests r JOIN inference_request_correlations c ON c.inference_request_id=r.id WHERE c.request_id=?`, requestID)
	record, err := scanEnrichedRequest(row)
	if err != nil {
		return CorrelatedRequestRecord{}, err
	}
	return CorrelatedRequestRecord{RequestRecord: record, RequestBody: record.RequestBody, ResponseBody: record.ResponseBody}, nil
}

// StoredOpenAIResponse is the Manager-side lookup row for OpenAI Responses
// retrieve/delete/input-items. These fields are not part of /logs DTOs.
type StoredOpenAIResponse struct {
	InstanceID   string
	Endpoint     string
	Streaming    bool
	Deleted      bool
	StartedAt    int64
	RequestBody  *string
	ResponseBody *string
}

func (s *Service) SetOpenAIResponseID(ctx context.Context, requestID, openaiID string) error {
	requestID = strings.TrimSpace(requestID)
	openaiID = strings.TrimSpace(openaiID)
	if requestID == "" || openaiID == "" {
		return nil
	}
	if handled, err := s.bufferOpenAIResponseID(requestID, openaiID); handled {
		return err
	}
	if s.writebackEnabled() {
		return nil
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE inference_requests SET openai_response_id=?
		WHERE id=(SELECT inference_request_id FROM inference_request_correlations WHERE request_id=?)
		AND (openai_response_id IS NULL OR openai_response_id='')`, openaiID, requestID)
	if err != nil && isUniqueConstraint(err) {
		return ErrDuplicateOpenAIResponseID
	}
	return err
}

func (s *Service) GetStoredOpenAIResponse(ctx context.Context, openaiID string) (StoredOpenAIResponse, error) {
	openaiID = strings.TrimSpace(openaiID)
	if openaiID == "" {
		return StoredOpenAIResponse{}, sql.ErrNoRows
	}
	if item, ok := s.bufferedStoredOpenAIResponse(openaiID); ok {
		return item, nil
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return StoredOpenAIResponse{}, err
	}
	var item StoredOpenAIResponse
	var deleted, streaming int
	var requestBody, responseBody sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT instance_id,endpoint,streaming,openai_response_deleted,started_at,request_body,response_body
		FROM inference_requests WHERE openai_response_id=? AND endpoint='/v1/responses'`, openaiID).Scan(
		&item.InstanceID, &item.Endpoint, &streaming, &deleted, &item.StartedAt, &requestBody, &responseBody)
	if err != nil {
		return StoredOpenAIResponse{}, err
	}
	item.Streaming = streaming != 0
	item.Deleted = deleted != 0
	if requestBody.Valid {
		value := requestBody.String
		item.RequestBody = &value
	}
	if responseBody.Valid {
		value := responseBody.String
		item.ResponseBody = &value
	}
	return item, nil
}

func (s *Service) MarkOpenAIResponseDeleted(ctx context.Context, openAIID string) error {
	openAIID = strings.TrimSpace(openAIID)
	if openAIID == "" {
		return sql.ErrNoRows
	}
	if handled, err := s.bufferMarkOpenAIResponseDeleted(openAIID); handled {
		return err
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE inference_requests SET openai_response_deleted=1
		WHERE openai_response_id=? AND endpoint='/v1/responses' AND openai_response_deleted=0`, openAIID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

var ErrDuplicateOpenAIResponseID = fmt.Errorf("duplicate openai response id")

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
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
