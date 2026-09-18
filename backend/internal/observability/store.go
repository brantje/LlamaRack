package observability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/brantje/llamarack/backend/internal/database"
)

// ObservabilityStore owns durable request, counter, and analytics persistence.
// Domain/service code keeps live in-memory state and policy; SQL stays here.
type ObservabilityStore interface {
	AddCounter(context.Context, Counter) error
	RecordRequest(context.Context, RequestRecord) error
	Counters(context.Context) ([]Counter, error)
	Summary(context.Context, int64) (Summary, []float64, []float64, error)
	ListRequests(context.Context, RequestFilters) ([]RequestRecord, error)
	Timeseries(context.Context, string, int64, int) ([]SeriesPoint, error)
	PruneRequests(context.Context, int64) error
	BeginCorrelatedRequest(context.Context, string, RequestRecord) error
	UpdateCorrelatedRequest(context.Context, string, RequestRecord) error
	FinalizeCorrelatedRequest(context.Context, string, *float64, RequestRecord) error
	GetRequestByRequestID(context.Context, string) (CorrelatedRequestRecord, error)
	SetOpenAIResponseID(context.Context, string, string) error
	GetStoredOpenAIResponse(context.Context, string) (StoredOpenAIResponse, error)
	MarkOpenAIResponseDeleted(context.Context, string) error
}

type sqlObservabilityStore struct {
	db database.Store
}

func NewObservabilityStore(db database.Store) ObservabilityStore {
	return &sqlObservabilityStore{db: db}
}

func (s *sqlObservabilityStore) AddCounter(ctx context.Context, counter Counter) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := addCounter(ctx, tx, counter); err != nil {
		return database.ClassifyError(err)
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlObservabilityStore) RecordRequest(ctx context.Context, record RequestRecord) error {
	var keyID, keyName, keyPrefix any
	if record.APIKey != nil {
		keyID, keyName, keyPrefix = record.APIKey.ID, record.APIKey.Name, record.APIKey.Prefix
	}
	var ttft, tps, requestBody, responseBody any
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
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var insertedID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO inference_requests(
		started_at,finished_at,instance_id,endpoint,api_key_id,api_key_name,api_key_prefix,streaming,status_code,result,
		duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,request_body,response_body
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id`,
		record.StartedAt, record.FinishedAt, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, boolInt(record.Streaming), record.StatusCode, record.Result,
		record.DurationMS, ttft, record.PromptTokens, record.GeneratedTokens, record.TotalTokens, tps, record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), record.Error, requestBody, responseBody).Scan(&insertedID); err != nil {
		return database.ClassifyError(err)
	}
	if record.ID == 0 {
		record.ID = insertedID
	}
	if err := addFinalCounters(ctx, tx, record); err != nil {
		return database.ClassifyError(err)
	}
	return database.ClassifyError(tx.Commit())
}

func addFinalCounters(ctx context.Context, tx database.Querier, record RequestRecord) error {
	if err := addCounter(ctx, tx, Counter{
		Metric: "gateway_requests_total", InstanceID: record.InstanceID, Endpoint: record.Endpoint,
		StatusCode: record.StatusCode, Result: record.Result, Streaming: record.Streaming, Value: 1,
	}); err != nil {
		return err
	}
	for metric, value := range map[string]int64{
		"prompt_tokens_total": record.PromptTokens,
		"generated_tokens_total": record.GeneratedTokens,
		"tokens_total": record.TotalTokens,
	} {
		if value > 0 {
			if err := addCounter(ctx, tx, Counter{
				Metric: metric, InstanceID: record.InstanceID, Endpoint: record.Endpoint,
				Streaming: record.Streaming, Value: float64(value),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func addCounter(ctx context.Context, tx database.Querier, counter Counter) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO observability_counters(metric,instance_id,endpoint,status_code,result,streaming,value)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT(metric,instance_id,endpoint,status_code,result,streaming)
		DO UPDATE SET value=observability_counters.value+excluded.value`,
		counter.Metric, counter.InstanceID, counter.Endpoint, counter.StatusCode, counter.Result, boolInt(counter.Streaming), counter.Value)
	return err
}

func (s *sqlObservabilityStore) Counters(ctx context.Context) ([]Counter, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT metric,instance_id,endpoint,status_code,result,streaming,value
		FROM observability_counters ORDER BY metric,instance_id,endpoint,status_code,result,streaming`)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := make([]Counter, 0)
	for rows.Next() {
		var item Counter
		var streaming int
		if err := rows.Scan(&item.Metric, &item.InstanceID, &item.Endpoint, &item.StatusCode, &item.Result, &streaming, &item.Value); err != nil {
			return nil, database.ClassifyError(err)
		}
		item.Streaming = streaming != 0
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlObservabilityStore) Summary(ctx context.Context, sinceMS int64) (Summary, []float64, []float64, error) {
	summary := Summary{Since: sinceMS}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN result='success' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN result='error' THEN 1 ELSE 0 END),0),
		COUNT(DISTINCT CASE WHEN api_key_id IS NOT NULL AND api_key_id<>'' THEN api_key_id END),
		COALESCE(SUM(prompt_tokens),0),COALESCE(SUM(generated_tokens),0),COALESCE(SUM(total_tokens),0)
		FROM inference_requests WHERE started_at>=? AND finished_at>0`, sinceMS).
		Scan(&summary.Requests, &summary.Successes, &summary.Errors, &summary.ActiveAPIKeys, &summary.PromptTokens, &summary.GeneratedTokens, &summary.TotalTokens); err != nil {
		return Summary{}, nil, nil, database.ClassifyError(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT duration_ms,ttft_ms FROM inference_requests
		WHERE started_at>=? AND finished_at>0 ORDER BY started_at`, sinceMS)
	if err != nil {
		return Summary{}, nil, nil, database.ClassifyError(err)
	}
	defer rows.Close()
	durations := make([]float64, 0)
	ttfts := make([]float64, 0)
	for rows.Next() {
		var duration float64
		var ttft sql.NullFloat64
		if err := rows.Scan(&duration, &ttft); err != nil {
			return Summary{}, nil, nil, database.ClassifyError(err)
		}
		durations = append(durations, duration)
		if ttft.Valid {
			ttfts = append(ttfts, ttft.Float64)
		}
	}
	if err := rows.Err(); err != nil {
		return Summary{}, nil, nil, database.ClassifyError(err)
	}
	return summary, durations, ttfts, nil
}

func (s *sqlObservabilityStore) ListRequests(ctx context.Context, filters RequestFilters) ([]RequestRecord, error) {
	query := `SELECT COALESCE(c.request_id,''),
		r.id,r.trace_id,r.call_type,r.started_at,r.finished_at,r.instance_id,r.endpoint,r.api_key_id,r.api_key_name,r.api_key_prefix,r.client_ip,r.user_agent,
		r.streaming,r.status_code,r.result,r.duration_ms,r.ttft_ms,r.prompt_tokens,r.generated_tokens,r.total_tokens,r.tokens_per_second,
		c.prompt_tokens_per_second,r.queue_duration_ms,r.load_duration_ms,r.autoloaded,r.error,NULL,NULL
		FROM inference_requests r LEFT JOIN inference_request_correlations c ON c.inference_request_id=r.id WHERE 1=1`
	args := []any{}
	add := func(clause string, value any) {
		query += clause
		args = append(args, value)
	}
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
		query += ` AND (LOWER(COALESCE(c.request_id,'')) LIKE LOWER(?) OR LOWER(r.trace_id) LIKE LOWER(?) OR LOWER(r.instance_id) LIKE LOWER(?) OR LOWER(r.endpoint) LIKE LOWER(?) OR LOWER(COALESCE(r.api_key_name,'')) LIKE LOWER(?) OR LOWER(COALESCE(r.api_key_prefix,'')) LIKE LOWER(?) OR LOWER(COALESCE(r.error,'')) LIKE LOWER(?) OR LOWER(r.client_ip) LIKE LOWER(?) OR LOWER(r.user_agent) LIKE LOWER(?))`
		for i := 0; i < 9; i++ {
			args = append(args, like)
		}
	}
	if filters.TraceID != "" {
		query += " ORDER BY r.started_at ASC,r.id ASC"
	} else {
		query += " ORDER BY r.started_at DESC,r.id DESC"
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, filters.Limit, filters.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := make([]RequestRecord, 0)
	for rows.Next() {
		item, err := scanEnrichedRequest(rows)
		if err != nil {
			return nil, database.ClassifyError(err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlObservabilityStore) Timeseries(ctx context.Context, metric string, sinceMS int64, bucketSeconds int) ([]SeriesPoint, error) {
	bucketMS := int64(bucketSeconds) * 1000
	expression := "COUNT(*)"
	completedOnly := ""
	switch metric {
	case "requests", "":
		completedOnly = " AND finished_at>0"
	case "latency":
		expression = "AVG(duration_ms)"
		completedOnly = " AND finished_at>0"
	case "ttft":
		expression = "AVG(ttft_ms)"
		completedOnly = " AND finished_at>0"
	case "tokens":
		expression = "COALESCE(SUM(total_tokens),0)"
		completedOnly = " AND finished_at>0"
	default:
		return nil, fmt.Errorf("unsupported metric %q", metric)
	}
	query := fmt.Sprintf(`SELECT (started_at / ?) * ? AS bucket,%s FROM inference_requests
		WHERE started_at>=?%s GROUP BY bucket ORDER BY bucket`, expression, completedOnly)
	rows, err := s.db.QueryContext(ctx, query, bucketMS, bucketMS, sinceMS)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := make([]SeriesPoint, 0)
	for rows.Next() {
		var point SeriesPoint
		var value sql.NullFloat64
		if err := rows.Scan(&point.Timestamp, &value); err != nil {
			return nil, database.ClassifyError(err)
		}
		if value.Valid {
			point.Value = value.Float64
		}
		out = append(out, point)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlObservabilityStore) PruneRequests(ctx context.Context, cutoff int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM inference_requests WHERE started_at<?`, cutoff)
	return database.ClassifyError(err)
}


func (s *sqlObservabilityStore) BeginCorrelatedRequest(ctx context.Context, requestID string, record RequestRecord) error {
	keyID, keyName, keyPrefix, ownerKind, ownerID, _, _, requestBody, _ := requestValues(record)
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var rowID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO inference_requests(
		started_at,finished_at,instance_id,endpoint,api_key_id,api_key_name,api_key_prefix,owner_kind,owner_id,streaming,status_code,result,
		duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,request_body,response_body,
		trace_id,call_type,client_ip,user_agent
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id`,
		record.StartedAt, 0, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, ownerKind, ownerID, boolInt(record.Streaming), 0, "pending",
		0, nil, 0, 0, 0, nil, 0, 0, 0, "", requestBody, nil,
		record.TraceID, record.CallType, record.ClientIP, record.UserAgent).Scan(&rowID); err != nil {
		return database.ClassifyError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_correlations(request_id,inference_request_id,prompt_tokens_per_second) VALUES(?,?,NULL)`, requestID, rowID); err != nil {
		return database.ClassifyError(err)
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlObservabilityStore) UpdateCorrelatedRequest(ctx context.Context, requestID string, record RequestRecord) error {
	keyID, keyName, keyPrefix, ownerKind, ownerID, _, _, requestBody, _ := requestValues(record)
	result, err := s.db.ExecContext(ctx, `UPDATE inference_requests SET
		instance_id=?,endpoint=?,api_key_id=?,api_key_name=?,api_key_prefix=?,owner_kind=?,owner_id=?,streaming=?,queue_duration_ms=?,load_duration_ms=?,autoloaded=?,request_body=?,
		trace_id=?,call_type=?,client_ip=?,user_agent=?
		WHERE id=(SELECT inference_request_id FROM inference_request_correlations WHERE request_id=?) AND finished_at=0`,
		record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, ownerKind, ownerID, boolInt(record.Streaming), record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), requestBody,
		record.TraceID, record.CallType, record.ClientIP, record.UserAgent, requestID)
	if err != nil {
		return database.ClassifyError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if affected == 0 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	return nil
}

func (s *sqlObservabilityStore) FinalizeCorrelatedRequest(ctx context.Context, requestID string, promptTokensPerSecond *float64, record RequestRecord) error {
	record = normalizeFinalRecord(record)
	keyID, keyName, keyPrefix, ownerKind, ownerID, ttft, tps, requestBody, responseBody := requestValues(record)
	var promptTPS any
	if promptTokensPerSecond != nil {
		promptTPS = *promptTokensPerSecond
	}
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE inference_requests SET
		started_at=?,finished_at=?,instance_id=?,endpoint=?,api_key_id=?,api_key_name=?,api_key_prefix=?,owner_kind=?,owner_id=?,streaming=?,status_code=?,result=?,duration_ms=?,ttft_ms=?,
		prompt_tokens=?,generated_tokens=?,total_tokens=?,tokens_per_second=?,queue_duration_ms=?,load_duration_ms=?,autoloaded=?,error=?,request_body=?,response_body=?,
		trace_id=?,call_type=?,client_ip=?,user_agent=?
		WHERE id=(SELECT inference_request_id FROM inference_request_correlations WHERE request_id=?) AND finished_at=0`,
		record.StartedAt, record.FinishedAt, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, ownerKind, ownerID, boolInt(record.Streaming), record.StatusCode, record.Result, record.DurationMS, ttft,
		record.PromptTokens, record.GeneratedTokens, record.TotalTokens, tps, record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), record.Error, requestBody, responseBody,
		record.TraceID, record.CallType, record.ClientIP, record.UserAgent, requestID)
	if err != nil {
		return database.ClassifyError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if affected == 0 {
		var existing int64
		err := tx.QueryRowContext(ctx, `SELECT inference_request_id FROM inference_request_correlations WHERE request_id=?`, requestID).Scan(&existing)
		if err == nil {
			return database.ClassifyError(tx.Commit())
		}
		if err != sql.ErrNoRows {
			return database.ClassifyError(err)
		}
		var rowID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO inference_requests(
			started_at,finished_at,instance_id,endpoint,api_key_id,api_key_name,api_key_prefix,owner_kind,owner_id,streaming,status_code,result,
			duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,request_body,response_body,
			trace_id,call_type,client_ip,user_agent
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id`,
			record.StartedAt, record.FinishedAt, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, ownerKind, ownerID, boolInt(record.Streaming), record.StatusCode, record.Result,
			record.DurationMS, ttft, record.PromptTokens, record.GeneratedTokens, record.TotalTokens, tps, record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), record.Error, requestBody, responseBody,
			record.TraceID, record.CallType, record.ClientIP, record.UserAgent).Scan(&rowID); err != nil {
			return database.ClassifyError(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_correlations(request_id,inference_request_id,prompt_tokens_per_second) VALUES(?,?,?)`, requestID, rowID, promptTPS); err != nil {
			return database.ClassifyError(err)
		}
		if err := addFinalCounters(ctx, tx, record); err != nil {
			return database.ClassifyError(err)
		}
		return database.ClassifyError(tx.Commit())
	}
	if _, err := tx.ExecContext(ctx, `UPDATE inference_request_correlations SET prompt_tokens_per_second=? WHERE request_id=?`, promptTPS, requestID); err != nil {
		return database.ClassifyError(err)
	}
	if err := addFinalCounters(ctx, tx, record); err != nil {
		return database.ClassifyError(err)
	}
	if record.Autoloaded {
		if err := addCounter(ctx, tx, Counter{Metric: "autoload_total", InstanceID: record.InstanceID, Value: 1}); err != nil {
			return database.ClassifyError(err)
		}
		if record.LoadDurationMS > 0 {
			if err := addCounter(ctx, tx, Counter{Metric: "load_duration_ms_total", InstanceID: record.InstanceID, Value: record.LoadDurationMS}); err != nil {
				return database.ClassifyError(err)
			}
		}
		if record.Result != "success" {
			if err := addCounter(ctx, tx, Counter{Metric: "failed_start_total", InstanceID: record.InstanceID, Value: 1}); err != nil {
				return database.ClassifyError(err)
			}
		}
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlObservabilityStore) GetRequestByRequestID(ctx context.Context, requestID string) (CorrelatedRequestRecord, error) {
	record, err := scanEnrichedRequest(s.db.QueryRowContext(ctx, `SELECT COALESCE(c.request_id,''),
		r.id,r.trace_id,r.call_type,r.started_at,r.finished_at,r.instance_id,r.endpoint,r.api_key_id,r.api_key_name,r.api_key_prefix,r.client_ip,r.user_agent,
		r.streaming,r.status_code,r.result,r.duration_ms,r.ttft_ms,r.prompt_tokens,r.generated_tokens,r.total_tokens,r.tokens_per_second,
		c.prompt_tokens_per_second,r.queue_duration_ms,r.load_duration_ms,r.autoloaded,r.error,r.request_body,r.response_body
		FROM inference_requests r JOIN inference_request_correlations c ON c.inference_request_id=r.id WHERE c.request_id=?`, requestID))
	if err != nil {
		return CorrelatedRequestRecord{}, database.ClassifyError(err)
	}
	return CorrelatedRequestRecord{RequestRecord: record, RequestBody: record.RequestBody, ResponseBody: record.ResponseBody}, nil
}

func (s *sqlObservabilityStore) SetOpenAIResponseID(ctx context.Context, requestID, openaiID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE inference_requests SET openai_response_id=?
		WHERE id=(SELECT inference_request_id FROM inference_request_correlations WHERE request_id=?)
		AND (openai_response_id IS NULL OR openai_response_id='')`, openaiID, requestID)
	err = database.ClassifyError(err)
	if errors.Is(err, database.ErrConflict) {
		return ErrDuplicateOpenAIResponseID
	}
	return err
}

func (s *sqlObservabilityStore) GetStoredOpenAIResponse(ctx context.Context, openaiID string) (StoredOpenAIResponse, error) {
	var item StoredOpenAIResponse
	var deleted, streaming int
	var requestBody, responseBody sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT instance_id,owner_kind,owner_id,endpoint,streaming,openai_response_deleted,started_at,request_body,response_body
		FROM inference_requests WHERE openai_response_id=? AND endpoint='/v1/responses'`, openaiID).Scan(
		&item.InstanceID, &item.OwnerKind, &item.OwnerID, &item.Endpoint, &streaming, &deleted, &item.StartedAt, &requestBody, &responseBody)
	if err != nil {
		return StoredOpenAIResponse{}, database.ClassifyError(err)
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

func (s *sqlObservabilityStore) MarkOpenAIResponseDeleted(ctx context.Context, openAIID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE inference_requests SET openai_response_deleted=1
		WHERE openai_response_id=? AND endpoint='/v1/responses' AND openai_response_deleted=0`, openAIID)
	if err != nil {
		return database.ClassifyError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return database.ClassifyError(err)
	}
	if affected == 0 {
		return database.ClassifyError(sql.ErrNoRows)
	}
	return nil
}

var _ ObservabilityStore = (*sqlObservabilityStore)(nil)
