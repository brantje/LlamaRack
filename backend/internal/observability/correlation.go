package observability

import (
	"context"
	"database/sql"
	"errors"
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
	if s.store == nil {
		return fmt.Errorf("database unavailable")
	}
	s.correlationReady = true
	return nil
}

func requestValues(record RequestRecord) (keyID, keyName, keyPrefix, ownerKind, ownerID, ttft, tps, requestBody, responseBody any) {
	if record.APIKey != nil {
		keyID, keyName, keyPrefix = record.APIKey.ID, record.APIKey.Name, record.APIKey.Prefix
	}
	ownerKind = ""
	ownerID = ""
	if strings.TrimSpace(record.OwnerKind) != "" && strings.TrimSpace(record.OwnerID) != "" {
		ownerKind = record.OwnerKind
		ownerID = record.OwnerID
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
	return s.store.BeginCorrelatedRequest(ctx, requestID, record)
}

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
	return s.store.UpdateCorrelatedRequest(ctx, requestID, record)
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
	return s.store.FinalizeCorrelatedRequest(ctx, requestID, promptTokensPerSecond, record)
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
	return s.store.GetRequestByRequestID(ctx, requestID)
}

// StoredOpenAIResponse is the Manager-side lookup row for OpenAI Responses
// retrieve/delete/input-items. These fields are not part of /logs DTOs.
type StoredOpenAIResponse struct {
	InstanceID   string
	OwnerKind    string
	OwnerID      string
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
	return s.store.SetOpenAIResponseID(ctx, requestID, openaiID)
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
	return s.store.GetStoredOpenAIResponse(ctx, openaiID)
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
	return s.store.MarkOpenAIResponseDeleted(ctx, openAIID)
}

var ErrDuplicateOpenAIResponseID = fmt.Errorf("duplicate openai response id")

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
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, record)
	})
}
