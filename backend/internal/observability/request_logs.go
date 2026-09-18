package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/brantje/llamarack/backend/internal/database"
)

// RequestLogRecord is the management/UI view of an inference request. It keeps
// the existing request metrics while adding the LiteLLM-compatible session
// grouping metadata and the model identity captured for the request.
type RequestLogRecord struct {
	RequestRecord
	SessionID         string `json:"session_id,omitempty"`
	SessionTotalCount int    `json:"session_total_count,omitempty"`
	ModelID           string `json:"model_id,omitempty"`
	ModelName         string `json:"model_name,omitempty"`
	ModelSlug         string `json:"model_slug,omitempty"`
}

// RequestLogDetail exposes retained payloads only for the selected request.
type RequestLogDetail struct {
	RequestLogRecord
	RequestBody  *string `json:"request_body,omitempty"`
	ResponseBody *string `json:"response_body,omitempty"`
}

// EnsureRequestLogSchema marks request-log schema as ready. Tables are created
// by embedded Goose migrations during database.Open.
func (s *Service) EnsureRequestLogSchema(ctx context.Context) error {
	return s.EnsureCorrelationSchema(ctx)
}

// UpdateRequestLogContext records grouping and model identity independently
// from request completion. Captured model identity remains available even if a
// Model or Instance is later renamed or removed.
func (s *Service) UpdateRequestLogContext(ctx context.Context, requestID, sessionID, instanceID string) error {
	if err := s.EnsureRequestLogSchema(ctx); err != nil {
		return err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("request_id is required")
	}
	return s.store.UpdateRequestLogContext(ctx, requestID, strings.TrimSpace(sessionID), strings.TrimSpace(instanceID))
}

func (s *Service) ListRequestLogs(ctx context.Context, filters RequestFilters, sessionID string) ([]RequestLogRecord, error) {
	if filters.Limit <= 0 || filters.Limit > 500 {
		filters.Limit = 100
	}
	if filters.Offset < 0 {
		filters.Offset = 0
	}
	if err := s.EnsureRequestLogSchema(ctx); err != nil {
		return nil, err
	}
	return s.store.ListRequestLogs(ctx, filters, strings.TrimSpace(sessionID))
}

func (s *Service) GetRequestLogByRequestID(ctx context.Context, requestID string) (RequestLogDetail, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return RequestLogDetail{}, fmt.Errorf("request_id is required")
	}
	if err := s.EnsureRequestLogSchema(ctx); err != nil {
		return RequestLogDetail{}, err
	}
	return s.store.GetRequestLogByRequestID(ctx, requestID)
}

func NewRequestLogsHandler(service *Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		filters, err := parseFilters(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		limit := filters.Limit
		if limit <= 0 {
			limit = 100
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
		queryFilters := filters
		queryFilters.Limit = limit
		if limit < 500 {
			queryFilters.Limit++
		}
		items, err := service.ListRequestLogs(r.Context(), queryFilters, sessionID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		hasMore := len(items) > limit
		if hasMore {
			items = items[:limit]
		} else if limit == 500 && len(items) == limit {
			probeFilters := filters
			probeFilters.Offset = filters.Offset + limit
			probeFilters.Limit = 1
			probe, probeErr := service.ListRequestLogs(r.Context(), probeFilters, sessionID)
			if probeErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": probeErr.Error()})
				return
			}
			hasMore = len(probe) > 0
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": filters.Offset, "has_more": hasMore})
	})
}

func NewRequestLogDetailHandler(service *Service) http.Handler {
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
		record, err := service.GetRequestLogByRequestID(r.Context(), requestID)
		if err != nil {
			if errors.Is(err, database.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "request not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, record)
	})
}
