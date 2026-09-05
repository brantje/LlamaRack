package observability

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

var (
	requestLogSchemaReady sync.Map
	requestLogSchemaMu    sync.Mutex
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
	if _, ok := requestLogSchemaReady.Load(s.db); ok {
		return nil
	}
	requestLogSchemaMu.Lock()
	defer requestLogSchemaMu.Unlock()
	if _, ok := requestLogSchemaReady.Load(s.db); ok {
		return nil
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return err
	}
	requestLogSchemaReady.Store(s.db, struct{}{})
	return nil
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
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM inference_request_correlations WHERE request_id=?`, requestID).Scan(&exists); err != nil {
		return err
	}
	sessionID = strings.TrimSpace(sessionID)
	var modelID, modelName string
	if instanceID = strings.TrimSpace(instanceID); instanceID != "" {
		err := s.db.QueryRowContext(ctx, `SELECT i.model_id,m.name FROM instances i JOIN models m ON m.id=i.model_id WHERE i.id=?`, instanceID).Scan(&modelID, &modelName)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO inference_request_log_context(request_id,session_id,model_id,model_name)
		VALUES(?,?,?,?)
		ON CONFLICT(request_id) DO UPDATE SET
			session_id=CASE WHEN excluded.session_id<>'' THEN excluded.session_id ELSE inference_request_log_context.session_id END,
			model_id=CASE WHEN excluded.model_id<>'' THEN excluded.model_id ELSE inference_request_log_context.model_id END,
			model_name=CASE WHEN excluded.model_name<>'' THEN excluded.model_name ELSE inference_request_log_context.model_name END`,
		requestID, sessionID, modelID, modelName)
	return err
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

	selectSQL := `SELECT COALESCE(c.request_id,''),
		r.id,r.trace_id,r.call_type,r.started_at,r.finished_at,r.instance_id,r.endpoint,r.api_key_id,r.api_key_name,r.api_key_prefix,r.client_ip,r.user_agent,
		r.streaming,r.status_code,r.result,r.duration_ms,r.ttft_ms,r.prompt_tokens,r.generated_tokens,r.total_tokens,r.tokens_per_second,
		c.prompt_tokens_per_second,r.queue_duration_ms,r.load_duration_ms,r.autoloaded,r.error,NULL,NULL,
		COALESCE(x.session_id,''),COALESCE(x.model_id,''),COALESCE(x.model_name,''),COALESCE(r.model_slug,''),
		CASE WHEN COALESCE(x.session_id,'')<>'' THEN (SELECT COUNT(*) FROM inference_request_log_context sx WHERE sx.session_id=x.session_id) ELSE 1 END
		FROM inference_requests r
		LEFT JOIN inference_request_correlations c ON c.inference_request_id=r.id
		LEFT JOIN inference_request_log_context x ON x.request_id=c.request_id`
	whereSQL := " WHERE 1=1"
	var args []any
	add := func(clause string, value any) { whereSQL += clause; args = append(args, value) }
	if filters.SinceMS > 0 {
		add(" AND r.started_at>=?", filters.SinceMS)
	}
	if filters.BeforeMS > 0 {
		add(" AND r.started_at<?", filters.BeforeMS)
	}
	if filters.InstanceID != "" {
		add(" AND r.instance_id=?", filters.InstanceID)
	}
	if filters.Endpoint != "" {
		add(" AND r.endpoint=?", filters.Endpoint)
	}
	if filters.APIKeyID != "" {
		add(" AND r.api_key_id=?", filters.APIKeyID)
	}
	if filters.Result != "" {
		add(" AND r.result=?", filters.Result)
	}
	if filters.StatusCode > 0 {
		add(" AND r.status_code=?", filters.StatusCode)
	}
	if filters.Streaming != nil {
		add(" AND r.streaming=?", boolInt(*filters.Streaming))
	}
	if filters.RequestID != "" {
		add(" AND c.request_id=?", filters.RequestID)
	}
	if filters.TraceID != "" {
		add(" AND r.trace_id=?", filters.TraceID)
	}
	if search := strings.TrimSpace(filters.Search); search != "" {
		like := "%" + search + "%"
		whereSQL += ` AND (c.request_id LIKE ? OR r.trace_id LIKE ? OR x.session_id LIKE ? OR r.instance_id LIKE ? OR r.model_slug LIKE ? OR x.model_id LIKE ? OR x.model_name LIKE ? OR r.endpoint LIKE ? OR COALESCE(r.api_key_name,'') LIKE ? OR COALESCE(r.api_key_prefix,'') LIKE ? OR COALESCE(r.error,'') LIKE ? OR r.client_ip LIKE ? OR r.user_agent LIKE ?)`
		for i := 0; i < 13; i++ {
			args = append(args, like)
		}
	}

	order := "DESC"
	if filters.TraceID != "" {
		order = "ASC"
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		whereSQL += " AND x.session_id=?"
		args = append(args, sessionID)
	}
	query := selectSQL + whereSQL + " ORDER BY r.started_at " + order + ",r.id " + order + " LIMIT ? OFFSET ?"
	args = append(args, filters.Limit, filters.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestLogRecord
	for rows.Next() {
		item, err := scanRequestLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanRequestLog(row interface{ Scan(...any) error }) (RequestLogRecord, error) {
	var item RequestLogRecord
	var keyID, keyName, keyPrefix, errText, requestBody, responseBody sql.NullString
	var streaming, autoloaded int
	var ttft, tps, promptTPS sql.NullFloat64
	if err := row.Scan(
		&item.RequestID, &item.ID, &item.TraceID, &item.CallType, &item.StartedAt, &item.FinishedAt, &item.InstanceID, &item.Endpoint,
		&keyID, &keyName, &keyPrefix, &item.ClientIP, &item.UserAgent, &streaming, &item.StatusCode, &item.Result, &item.DurationMS,
		&ttft, &item.PromptTokens, &item.GeneratedTokens, &item.TotalTokens, &tps, &promptTPS, &item.QueueDurationMS, &item.LoadDurationMS,
		&autoloaded, &errText, &requestBody, &responseBody, &item.SessionID, &item.ModelID, &item.ModelName, &item.ModelSlug, &item.SessionTotalCount,
	); err != nil {
		return RequestLogRecord{}, err
	}
	item.Streaming = streaming != 0
	item.Autoloaded = autoloaded != 0
	if keyID.Valid || keyName.Valid || keyPrefix.Valid {
		item.APIKey = &APIKeyRef{ID: keyID.String, Name: keyName.String, Prefix: keyPrefix.String}
	}
	if ttft.Valid {
		value := ttft.Float64
		item.TTFTMS = &value
	}
	if tps.Valid {
		value := tps.Float64
		item.TokensPerSecond = &value
		generation := value
		item.GenerationTokensPerSecond = &generation
	}
	if promptTPS.Valid {
		value := promptTPS.Float64
		item.PromptTokensPerSecond = &value
	}
	if errText.Valid {
		item.Error = errText.String
	}
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

func (s *Service) GetRequestLogByRequestID(ctx context.Context, requestID string) (RequestLogDetail, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return RequestLogDetail{}, fmt.Errorf("request_id is required")
	}
	if err := s.EnsureRequestLogSchema(ctx); err != nil {
		return RequestLogDetail{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT COALESCE(c.request_id,''),
		r.id,r.trace_id,r.call_type,r.started_at,r.finished_at,r.instance_id,r.endpoint,r.api_key_id,r.api_key_name,r.api_key_prefix,r.client_ip,r.user_agent,
		r.streaming,r.status_code,r.result,r.duration_ms,r.ttft_ms,r.prompt_tokens,r.generated_tokens,r.total_tokens,r.tokens_per_second,
		c.prompt_tokens_per_second,r.queue_duration_ms,r.load_duration_ms,r.autoloaded,r.error,r.request_body,r.response_body,
		COALESCE(x.session_id,''),COALESCE(x.model_id,''),COALESCE(x.model_name,''),COALESCE(r.model_slug,''),
		CASE WHEN COALESCE(x.session_id,'')<>'' THEN (SELECT COUNT(*) FROM inference_request_log_context sx WHERE sx.session_id=x.session_id) ELSE 1 END
		FROM inference_requests r
		JOIN inference_request_correlations c ON c.inference_request_id=r.id
		LEFT JOIN inference_request_log_context x ON x.request_id=c.request_id
		WHERE c.request_id=?`, requestID)
	record, err := scanRequestLog(row)
	if err != nil {
		return RequestLogDetail{}, err
	}
	return RequestLogDetail{RequestLogRecord: record, RequestBody: record.RequestBody, ResponseBody: record.ResponseBody}, nil
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
