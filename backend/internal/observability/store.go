package observability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/telemetry"
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
	RecordHardware(context.Context, hardware.Snapshot, []telemetry.Sample, int64) error
	HardwareTimeseries(context.Context, string, int64, int, string, string) ([]HardwareSeriesPoint, error)
	LifecycleSummary(context.Context, int64) (LifecycleSummary, error)
	PruneHardware(context.Context, int64) error
	RequestTimeseries(context.Context, string, int64, int, string) ([]SeriesPoint, error)
	RecordContextMetrics(context.Context, int64, []RuntimeTelemetrySample) error
	RecordLifecycleCounters(context.Context, string, string, float64) error
	SetRequestModelSlug(context.Context, string, string) error
	RequestModelIdentity(context.Context, string) (RequestModelIdentity, error)
	RecordPlaygroundLifecycleEvent(context.Context, string, string, string) error
	PlaygroundEvictions(context.Context, string, string) ([]string, error)
	StageInferenceTurnStats(context.Context, string, InferenceTurnStats) error
	SaveInferenceTurnStats(context.Context, string, InferenceTurnStats) error
	InferenceTurnStats(context.Context, string) (*InferenceTurnStats, error)
	UpdateRequestLogContext(context.Context, string, string, string) error
	ListRequestLogs(context.Context, RequestFilters, string) ([]RequestLogRecord, error)
	GetRequestLogByRequestID(context.Context, string) (RequestLogDetail, error)
	PersistWritebackBatch(context.Context, []writebackEntry) error
}

type sqlObservabilityStore struct {
	db database.Store

	modelIdentityMu sync.Mutex
	modelIdentities map[string]writebackModelIdentity
}

func NewObservabilityStore(db database.Store) ObservabilityStore {
	if db == nil {
		return nil
	}
	return &sqlObservabilityStore{db: db, modelIdentities: map[string]writebackModelIdentity{}}
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


func (s *sqlObservabilityStore) RecordHardware(ctx context.Context, snapshot hardware.Snapshot, samples []telemetry.Sample, timestamp int64) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insert := func(metric, deviceID, instanceID string, value float64) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO hardware_metric_samples(collected_at,metric,device_id,instance_id,value) VALUES(?,?,?,?,?)`,
			timestamp, metric, deviceID, instanceID, value)
		return err
	}
	if snapshot.RAMTotalBytes > 0 {
		if err := insert("ram_total_bytes", "", "", float64(snapshot.RAMTotalBytes)); err != nil {
			return database.ClassifyError(err)
		}
		used := snapshot.RAMTotalBytes - snapshot.RAMAvailableBytes
		if used < 0 {
			used = 0
		}
		if err := insert("ram_used_bytes", "", "", float64(used)); err != nil {
			return database.ClassifyError(err)
		}
	}
	var totalVRAM, usedVRAM int64
	var utilization float64
	for _, gpu := range snapshot.GPUs {
		totalVRAM += gpu.TotalBytes
		usedVRAM += gpu.UsedBytes
		utilization += gpu.UtilizationPct
		if err := insert("vram_total_bytes", gpu.ID, "", float64(gpu.TotalBytes)); err != nil {
			return database.ClassifyError(err)
		}
		if err := insert("vram_used_bytes", gpu.ID, "", float64(gpu.UsedBytes)); err != nil {
			return database.ClassifyError(err)
		}
		if err := insert("gpu_utilization_pct", gpu.ID, "", gpu.UtilizationPct); err != nil {
			return database.ClassifyError(err)
		}
	}
	if len(snapshot.GPUs) > 0 {
		if err := insert("vram_total_bytes", "", "", float64(totalVRAM)); err != nil {
			return database.ClassifyError(err)
		}
		if err := insert("vram_used_bytes", "", "", float64(usedVRAM)); err != nil {
			return database.ClassifyError(err)
		}
		if err := insert("gpu_utilization_pct", "", "", utilization/float64(len(snapshot.GPUs))); err != nil {
			return database.ClassifyError(err)
		}
	}
	for _, sample := range samples {
		if sample.VRAMUsedBytes != nil {
			if err := insert("instance_vram_used_bytes", "", sample.InstanceID, float64(*sample.VRAMUsedBytes)); err != nil {
				return database.ClassifyError(err)
			}
		}
		if sample.CPUPercent != nil {
			if err := insert("instance_cpu_percent", "", sample.InstanceID, *sample.CPUPercent); err != nil {
				return database.ClassifyError(err)
			}
		}
		if sample.MemoryUsedBytes != nil {
			if err := insert("instance_memory_used_bytes", "", sample.InstanceID, float64(*sample.MemoryUsedBytes)); err != nil {
				return database.ClassifyError(err)
			}
		}
		for _, gpu := range sample.GPUs {
			if gpu.VRAMUsedBytes != nil {
				if err := insert("instance_vram_used_bytes", gpu.DeviceID, sample.InstanceID, float64(*gpu.VRAMUsedBytes)); err != nil {
					return database.ClassifyError(err)
				}
			}
		}
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlObservabilityStore) HardwareTimeseries(ctx context.Context, metric string, sinceMS int64, bucketSeconds int, deviceID, instanceID string) ([]HardwareSeriesPoint, error) {
	bucketMS := int64(bucketSeconds) * 1000
	query := `SELECT (collected_at / ?) * ? AS bucket,device_id,instance_id,AVG(value)
		FROM hardware_metric_samples WHERE metric=? AND collected_at>=?`
	args := []any{bucketMS, bucketMS, metric, sinceMS}
	if deviceID != "" {
		query += " AND device_id=?"
		args = append(args, deviceID)
	}
	if instanceID != "" {
		query += " AND instance_id=?"
		args = append(args, instanceID)
	}
	if deviceID == "" && instanceID == "" {
		query += " AND device_id='' AND instance_id=''"
	}
	query += " GROUP BY bucket,device_id,instance_id ORDER BY bucket,device_id,instance_id"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := make([]HardwareSeriesPoint, 0)
	for rows.Next() {
		var point HardwareSeriesPoint
		var value sql.NullFloat64
		if err := rows.Scan(&point.Timestamp, &point.DeviceID, &point.InstanceID, &value); err != nil {
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

func (s *sqlObservabilityStore) LifecycleSummary(ctx context.Context, sinceMS int64) (LifecycleSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT metric,COALESCE(SUM(value),0)
		FROM observability_counters WHERE metric IN ('load_total','eviction_total','idle_unload_total') GROUP BY metric`)
	if err != nil {
		return LifecycleSummary{}, database.ClassifyError(err)
	}
	defer rows.Close()
	var summary LifecycleSummary
	for rows.Next() {
		var metric string
		var value float64
		if err := rows.Scan(&metric, &value); err != nil {
			return LifecycleSummary{}, database.ClassifyError(err)
		}
		switch strings.TrimSpace(metric) {
		case "load_total":
			summary.Loads = int64(value)
		case "eviction_total":
			summary.Evictions = int64(value)
		case "idle_unload_total":
			summary.IdleUnloads = int64(value)
		}
	}
	if err := rows.Err(); err != nil {
		return LifecycleSummary{}, database.ClassifyError(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN finished_at>0 AND result<>'success' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN finished_at>0 THEN load_duration_ms ELSE 0 END),0)
		FROM inference_requests WHERE autoloaded=1 AND started_at>=?`, sinceMS).
		Scan(&summary.Autoloads, &summary.FailedStarts, &summary.LoadMS); err != nil {
		return LifecycleSummary{}, database.ClassifyError(err)
	}
	return summary, nil
}

func (s *sqlObservabilityStore) PruneHardware(ctx context.Context, cutoff int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM hardware_metric_samples WHERE collected_at<?`, cutoff)
	return database.ClassifyError(err)
}

func (s *sqlObservabilityStore) RequestTimeseries(ctx context.Context, metric string, sinceMS int64, bucketSeconds int, instanceID string) ([]SeriesPoint, error) {
	bucketMS := int64(bucketSeconds) * 1000
	if metric == "latency_p50" || metric == "latency_p95" {
		query := `SELECT started_at,duration_ms FROM inference_requests WHERE started_at>=? AND finished_at>0`
		args := []any{sinceMS}
		if instanceID != "" {
			query += " AND instance_id=?"
			args = append(args, instanceID)
		}
		query += " ORDER BY started_at"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, database.ClassifyError(err)
		}
		defer rows.Close()
		buckets := map[int64][]float64{}
		for rows.Next() {
			var startedAt int64
			var duration float64
			if err := rows.Scan(&startedAt, &duration); err != nil {
				return nil, database.ClassifyError(err)
			}
			bucket := (startedAt / bucketMS) * bucketMS
			buckets[bucket] = append(buckets[bucket], duration)
		}
		if err := rows.Err(); err != nil {
			return nil, database.ClassifyError(err)
		}
		keys := make([]int64, 0, len(buckets))
		for bucket := range buckets {
			keys = append(keys, bucket)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		out := make([]SeriesPoint, 0, len(keys))
		for _, bucket := range keys {
			values := percentiles(buckets[bucket])
			selected := values.P50
			if metric == "latency_p95" {
				selected = values.P95
			}
			if selected != nil {
				out = append(out, SeriesPoint{Timestamp: bucket, Value: *selected})
			}
		}
		return out, nil
	}
	if metric == "instance_context_tokens_max" {
		query := `SELECT (collected_at / ?) * ? AS bucket,MAX(value)
			FROM hardware_metric_samples WHERE metric='instance_context_tokens_max' AND collected_at>=?`
		args := []any{bucketMS, bucketMS, sinceMS}
		if instanceID != "" {
			query += " AND instance_id=?"
			args = append(args, instanceID)
		}
		query += " GROUP BY bucket ORDER BY bucket"
		return scanStoreSeriesRows(s.db.QueryContext(ctx, query, args...))
	}
	expression := ""
	switch metric {
	case "requests":
		expression = "COUNT(*)"
	case "latency":
		expression = "AVG(duration_ms)"
	case "ttft":
		expression = "AVG(ttft_ms)"
	case "tokens":
		expression = "COALESCE(SUM(total_tokens),0)"
	case "prompt_tokens":
		expression = "COALESCE(SUM(prompt_tokens),0)"
	case "generated_tokens":
		expression = "COALESCE(SUM(generated_tokens),0)"
	default:
		return nil, fmt.Errorf("unsupported metric %q", metric)
	}
	query := fmt.Sprintf(`SELECT (started_at / ?) * ? AS bucket,%s
		FROM inference_requests WHERE started_at>=? AND finished_at>0`, expression)
	args := []any{bucketMS, bucketMS, sinceMS}
	if instanceID != "" {
		query += " AND instance_id=?"
		args = append(args, instanceID)
	}
	query += " GROUP BY bucket ORDER BY bucket"
	return scanStoreSeriesRows(s.db.QueryContext(ctx, query, args...))
}

func scanStoreSeriesRows(rows *sql.Rows, err error) ([]SeriesPoint, error) {
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

func (s *sqlObservabilityStore) RecordContextMetrics(ctx context.Context, timestamp int64, samples []RuntimeTelemetrySample) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, sample := range samples {
		if sample.InstanceID == "" || sample.LlamaMetrics == nil || sample.LlamaMetrics.ContextTokensMax == nil {
			continue
		}
		value := *sample.LlamaMetrics.ContextTokensMax
		if value < 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO hardware_metric_samples(collected_at,metric,device_id,instance_id,value) VALUES(?,?,?,?,?)`,
			timestamp, "instance_context_tokens_max", "", sample.InstanceID, value); err != nil {
			return database.ClassifyError(err)
		}
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlObservabilityStore) RecordLifecycleCounters(ctx context.Context, event, instanceID string, durationMS float64) error {
	metric := ""
	switch event {
	case LifecycleLoad:
		metric = "load_total"
	case LifecycleFailedStart:
		metric = "failed_start_total"
	case LifecycleEviction:
		metric = "eviction_total"
	case LifecycleIdleUnload:
		metric = "idle_unload_total"
	default:
		return fmt.Errorf("unsupported lifecycle event %q", event)
	}
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := addCounter(ctx, tx, Counter{Metric: metric, InstanceID: instanceID, Value: 1}); err != nil {
		return database.ClassifyError(err)
	}
	if event == LifecycleLoad && durationMS > 0 {
		if err := addCounter(ctx, tx, Counter{Metric: "load_duration_ms_total", InstanceID: instanceID, Value: durationMS}); err != nil {
			return database.ClassifyError(err)
		}
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlObservabilityStore) SetRequestModelSlug(ctx context.Context, requestID, modelSlug string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE inference_requests SET model_slug=?
		WHERE id=(SELECT inference_request_id FROM inference_request_correlations WHERE request_id=?)`, modelSlug, requestID)
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

func (s *sqlObservabilityStore) RequestModelIdentity(ctx context.Context, requestID string) (RequestModelIdentity, error) {
	var identity RequestModelIdentity
	err := s.db.QueryRowContext(ctx, `SELECT r.instance_id,r.model_slug
		FROM inference_requests r JOIN inference_request_correlations c ON c.inference_request_id=r.id
		WHERE c.request_id=?`, requestID).Scan(&identity.InstanceID, &identity.ModelSlug)
	return identity, database.ClassifyError(err)
}


func (s *sqlObservabilityStore) RecordPlaygroundLifecycleEvent(ctx context.Context, event, instanceID, correlationID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO playground_lifecycle_events(event,instance_id,correlation_id) VALUES(?,?,?)`, event, instanceID, correlationID)
	return database.ClassifyError(err)
}

func (s *sqlObservabilityStore) PlaygroundEvictions(ctx context.Context, correlationID, excludeInstanceID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id FROM playground_lifecycle_events
		WHERE event=? AND correlation_id=? AND instance_id<>? ORDER BY id`, LifecycleEviction, correlationID, excludeInstanceID)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	out := make([]string, 0)
	for rows.Next() {
		var instanceID string
		if err := rows.Scan(&instanceID); err != nil {
			return nil, database.ClassifyError(err)
		}
		instanceID = strings.TrimSpace(instanceID)
		if instanceID == "" || seen[instanceID] {
			continue
		}
		seen[instanceID] = true
		out = append(out, instanceID)
	}
	if err := rows.Err(); err != nil {
		return nil, database.ClassifyError(err)
	}
	return out, nil
}

func (s *sqlObservabilityStore) StageInferenceTurnStats(ctx context.Context, requestID string, stats InferenceTurnStats) error {
	return s.stageInferenceTurnStats(ctx, requestID, stats)
}

func (s *sqlObservabilityStore) SaveInferenceTurnStats(ctx context.Context, requestID string, stats InferenceTurnStats) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM inference_request_correlations WHERE request_id=?`, requestID).Scan(&exists); err != nil {
		return database.ClassifyError(err)
	}
	return s.stageInferenceTurnStats(ctx, requestID, stats)
}

func (s *sqlObservabilityStore) stageInferenceTurnStats(ctx context.Context, requestID string, stats InferenceTurnStats) error {
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
		nullableValue(stats.CacheN), nullableValue(stats.DraftN), nullableValue(stats.DraftNAccepted), nullableValue(stats.FinishReason), nullableValue(stats.ToolCallCount))
	return database.ClassifyError(err)
}

func (s *sqlObservabilityStore) InferenceTurnStats(ctx context.Context, requestID string) (*InferenceTurnStats, error) {
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, database.ClassifyError(err)
	}
	return &InferenceTurnStats{
		PromptN: nullInt64Ptr(promptN), PromptMS: nullFloat64Ptr(promptMS),
		PromptPerSecond: nullFloat64Ptr(promptPerSecond), PromptPerTokenMS: nullFloat64Ptr(promptPerTokenMS),
		PredictedN: nullInt64Ptr(predictedN), PredictedMS: nullFloat64Ptr(predictedMS),
		PredictedPerSecond: nullFloat64Ptr(predictedPerSecond), PredictedPerTokenMS: nullFloat64Ptr(predictedPerTokenMS),
		CacheN: nullInt64Ptr(cacheN), DraftN: nullInt64Ptr(draftN), DraftNAccepted: nullInt64Ptr(draftNAccepted),
		FinishReason: nullStringPtr(finishReason), ToolCallCount: nullInt64Ptr(toolCallCount),
	}, nil
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

func (s *sqlObservabilityStore) UpdateRequestLogContext(ctx context.Context, requestID, sessionID, instanceID string) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM inference_request_correlations WHERE request_id=?`, requestID).Scan(&exists); err != nil {
		return database.ClassifyError(err)
	}
	var modelID, modelName string
	if instanceID != "" {
		err := s.db.QueryRowContext(ctx, `SELECT i.model_id,m.name FROM instances i JOIN models m ON m.id=i.model_id WHERE i.id=?`, instanceID).Scan(&modelID, &modelName)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return database.ClassifyError(err)
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO inference_request_log_context(request_id,session_id,model_id,model_name)
		VALUES(?,?,?,?)
		ON CONFLICT(request_id) DO UPDATE SET
			session_id=CASE WHEN excluded.session_id<>'' THEN excluded.session_id ELSE inference_request_log_context.session_id END,
			model_id=CASE WHEN excluded.model_id<>'' THEN excluded.model_id ELSE inference_request_log_context.model_id END,
			model_name=CASE WHEN excluded.model_name<>'' THEN excluded.model_name ELSE inference_request_log_context.model_name END`,
		requestID, sessionID, modelID, modelName)
	return database.ClassifyError(err)
}

func (s *sqlObservabilityStore) ListRequestLogs(ctx context.Context, filters RequestFilters, sessionID string) ([]RequestLogRecord, error) {
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
	args := []any{}
	add := func(clause string, value any) {
		whereSQL += clause
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
		whereSQL += ` AND (LOWER(COALESCE(c.request_id,'')) LIKE LOWER(?) OR LOWER(r.trace_id) LIKE LOWER(?) OR LOWER(COALESCE(x.session_id,'')) LIKE LOWER(?) OR LOWER(r.instance_id) LIKE LOWER(?) OR LOWER(r.model_slug) LIKE LOWER(?) OR LOWER(COALESCE(x.model_id,'')) LIKE LOWER(?) OR LOWER(COALESCE(x.model_name,'')) LIKE LOWER(?) OR LOWER(r.endpoint) LIKE LOWER(?) OR LOWER(COALESCE(r.api_key_name,'')) LIKE LOWER(?) OR LOWER(COALESCE(r.api_key_prefix,'')) LIKE LOWER(?) OR LOWER(COALESCE(r.error,'')) LIKE LOWER(?) OR LOWER(r.client_ip) LIKE LOWER(?) OR LOWER(r.user_agent) LIKE LOWER(?))`
		for i := 0; i < 13; i++ {
			args = append(args, like)
		}
	}
	order := "DESC"
	if filters.TraceID != "" {
		order = "ASC"
	}
	if sessionID != "" {
		whereSQL += " AND x.session_id=?"
		args = append(args, sessionID)
	}
	query := selectSQL + whereSQL + " ORDER BY r.started_at " + order + ",r.id " + order + " LIMIT ? OFFSET ?"
	args = append(args, filters.Limit, filters.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, database.ClassifyError(err)
	}
	defer rows.Close()
	out := make([]RequestLogRecord, 0)
	for rows.Next() {
		item, err := scanRequestLog(rows)
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

func (s *sqlObservabilityStore) GetRequestLogByRequestID(ctx context.Context, requestID string) (RequestLogDetail, error) {
	record, err := scanRequestLog(s.db.QueryRowContext(ctx, `SELECT COALESCE(c.request_id,''),
		r.id,r.trace_id,r.call_type,r.started_at,r.finished_at,r.instance_id,r.endpoint,r.api_key_id,r.api_key_name,r.api_key_prefix,r.client_ip,r.user_agent,
		r.streaming,r.status_code,r.result,r.duration_ms,r.ttft_ms,r.prompt_tokens,r.generated_tokens,r.total_tokens,r.tokens_per_second,
		c.prompt_tokens_per_second,r.queue_duration_ms,r.load_duration_ms,r.autoloaded,r.error,r.request_body,r.response_body,
		COALESCE(x.session_id,''),COALESCE(x.model_id,''),COALESCE(x.model_name,''),COALESCE(r.model_slug,''),
		CASE WHEN COALESCE(x.session_id,'')<>'' THEN (SELECT COUNT(*) FROM inference_request_log_context sx WHERE sx.session_id=x.session_id) ELSE 1 END
		FROM inference_requests r
		JOIN inference_request_correlations c ON c.inference_request_id=r.id
		LEFT JOIN inference_request_log_context x ON x.request_id=c.request_id
		WHERE c.request_id=?`, requestID))
	if err != nil {
		return RequestLogDetail{}, database.ClassifyError(err)
	}
	return RequestLogDetail{RequestLogRecord: record, RequestBody: record.RequestBody, ResponseBody: record.ResponseBody}, nil
}


func (s *sqlObservabilityStore) PersistWritebackBatch(ctx context.Context, batch []writebackEntry) error {
	tx, err := database.Begin(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i := range batch {
		if err := s.persistWritebackEntry(ctx, tx, batch[i]); err != nil {
			return database.ClassifyError(err)
		}
	}
	return database.ClassifyError(tx.Commit())
}

func (s *sqlObservabilityStore) persistWritebackEntry(ctx context.Context, tx database.Querier, entry writebackEntry) error {
	record := normalizeFinalRecord(entry.record)
	keyID, keyName, keyPrefix, ownerKind, ownerID, ttft, tps, requestBody, responseBody := requestValues(record)
	var promptTPS any
	if entry.promptTPS != nil {
		promptTPS = *entry.promptTPS
	}

	var rowID int64
	var existingFinishedAt int64
	err := tx.QueryRowContext(ctx, `SELECT r.id,r.finished_at
		FROM inference_request_correlations c
		JOIN inference_requests r ON r.id=c.inference_request_id
		WHERE c.request_id=?`, entry.requestID).Scan(&rowID, &existingFinishedAt)
	existing := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var openAIID any
	if entry.openAIResponseID != "" {
		var ownerRequestID string
		ownerErr := tx.QueryRowContext(ctx, `SELECT COALESCE(c.request_id,'')
			FROM inference_requests r
			LEFT JOIN inference_request_correlations c ON c.inference_request_id=r.id
			WHERE r.openai_response_id=? LIMIT 1`, entry.openAIResponseID).Scan(&ownerRequestID)
		switch {
		case errors.Is(ownerErr, sql.ErrNoRows):
			openAIID = entry.openAIResponseID
		case ownerErr != nil:
			return ownerErr
		case ownerRequestID == entry.requestID:
			openAIID = entry.openAIResponseID
		default:
			slog.Warn("duplicate openai response id ignored during observability writeback", "request_id", entry.requestID, "openai_response_id", entry.openAIResponseID)
		}
	}

	if !existing {
		if err := tx.QueryRowContext(ctx, `INSERT INTO inference_requests(
			started_at,finished_at,instance_id,endpoint,api_key_id,api_key_name,api_key_prefix,owner_kind,owner_id,streaming,status_code,result,
			duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,request_body,response_body,
			trace_id,call_type,client_ip,user_agent,model_slug,openai_response_id,openai_response_deleted
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id`,
			record.StartedAt, record.FinishedAt, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, ownerKind, ownerID, boolInt(record.Streaming), record.StatusCode, record.Result,
			record.DurationMS, ttft, record.PromptTokens, record.GeneratedTokens, record.TotalTokens, tps, record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), record.Error, requestBody, responseBody,
			record.TraceID, record.CallType, record.ClientIP, record.UserAgent, entry.modelSlug, openAIID, boolInt(entry.openAIDeleted)).Scan(&rowID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_correlations(request_id,inference_request_id,prompt_tokens_per_second) VALUES(?,?,?)`, entry.requestID, rowID, promptTPS); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE inference_requests SET
			started_at=?,finished_at=?,instance_id=?,endpoint=?,api_key_id=?,api_key_name=?,api_key_prefix=?,owner_kind=?,owner_id=?,streaming=?,status_code=?,result=?,duration_ms=?,ttft_ms=?,
			prompt_tokens=?,generated_tokens=?,total_tokens=?,tokens_per_second=?,queue_duration_ms=?,load_duration_ms=?,autoloaded=?,error=?,request_body=?,response_body=?,
			trace_id=?,call_type=?,client_ip=?,user_agent=?,model_slug=?,openai_response_id=COALESCE(?,openai_response_id),
			openai_response_deleted=CASE WHEN openai_response_deleted=1 OR ?=1 THEN 1 ELSE 0 END
			WHERE id=?`,
			record.StartedAt, record.FinishedAt, record.InstanceID, record.Endpoint, keyID, keyName, keyPrefix, ownerKind, ownerID, boolInt(record.Streaming), record.StatusCode, record.Result, record.DurationMS, ttft,
			record.PromptTokens, record.GeneratedTokens, record.TotalTokens, tps, record.QueueDurationMS, record.LoadDurationMS, boolInt(record.Autoloaded), record.Error, requestBody, responseBody,
			record.TraceID, record.CallType, record.ClientIP, record.UserAgent, entry.modelSlug, openAIID, boolInt(entry.openAIDeleted), rowID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE inference_request_correlations SET prompt_tokens_per_second=? WHERE request_id=?`, promptTPS, entry.requestID); err != nil {
			return err
		}
	}

	if entry.contextReady {
		modelID, modelName, err := s.resolveWritebackModelIdentity(ctx, tx, record.InstanceID, entry.contextInstanceID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inference_request_log_context(request_id,session_id,model_id,model_name) VALUES(?,?,?,?)
			ON CONFLICT(request_id) DO UPDATE SET
				session_id=CASE WHEN excluded.session_id<>'' THEN excluded.session_id ELSE inference_request_log_context.session_id END,
				model_id=CASE WHEN excluded.model_id<>'' THEN excluded.model_id ELSE inference_request_log_context.model_id END,
				model_name=CASE WHEN excluded.model_name<>'' THEN excluded.model_name ELSE inference_request_log_context.model_name END`,
			entry.requestID, entry.sessionID, modelID, modelName); err != nil {
			return err
		}
	}

	if !existing || existingFinishedAt == 0 {
		if err := addFinalCounters(ctx, tx, record); err != nil {
			return err
		}
		if existing && record.Autoloaded {
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
	}
	return nil
}

func (s *sqlObservabilityStore) resolveWritebackModelIdentity(ctx context.Context, tx database.Querier, durableID, publicID string) (string, string, error) {
	durableID = strings.TrimSpace(durableID)
	publicID = strings.TrimSpace(publicID)
	if durableID == "" && publicID == "" {
		return "", "", nil
	}
	cacheKey := durableID
	if cacheKey == "" {
		cacheKey = "slug:" + publicID
	}
	s.modelIdentityMu.Lock()
	cached, ok := s.modelIdentities[cacheKey]
	s.modelIdentityMu.Unlock()
	if ok {
		return cached.modelID, cached.modelName, nil
	}
	var modelID, modelName string
	err := tx.QueryRowContext(ctx, `SELECT i.model_id,m.name
		FROM instances i JOIN models m ON m.id=i.model_id
		WHERE i.id=? OR i.slug=? LIMIT 1`, durableID, publicID).Scan(&modelID, &modelName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	s.modelIdentityMu.Lock()
	s.modelIdentities[cacheKey] = writebackModelIdentity{modelID: modelID, modelName: modelName}
	s.modelIdentityMu.Unlock()
	return modelID, modelName, nil
}

var _ ObservabilityStore = (*sqlObservabilityStore)(nil)

func scanEnrichedRequest(row interface{ Scan(...any) error }) (RequestRecord, error) {
	var item RequestRecord
	var keyID, keyName, keyPrefix, errText, requestBody, responseBody sql.NullString
	var streaming, autoloaded int
	var ttft, tps, promptTPS sql.NullFloat64
	if err := row.Scan(
		&item.RequestID, &item.ID, &item.TraceID, &item.CallType, &item.StartedAt, &item.FinishedAt, &item.InstanceID, &item.Endpoint,
		&keyID, &keyName, &keyPrefix, &item.ClientIP, &item.UserAgent, &streaming, &item.StatusCode, &item.Result, &item.DurationMS,
		&ttft, &item.PromptTokens, &item.GeneratedTokens, &item.TotalTokens, &tps, &promptTPS, &item.QueueDurationMS, &item.LoadDurationMS,
		&autoloaded, &errText, &requestBody, &responseBody,
	); err != nil {
		return RequestRecord{}, err
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

func scanRequest(row interface{ Scan(...any) error }) (RequestRecord, error) {
	var item RequestRecord
	var keyID, keyName, keyPrefix, errText, requestBody, responseBody sql.NullString
	var streaming, autoloaded int
	var ttft, tps sql.NullFloat64
	if err := row.Scan(&item.ID, &item.StartedAt, &item.FinishedAt, &item.InstanceID, &item.Endpoint, &keyID, &keyName, &keyPrefix, &streaming, &item.StatusCode, &item.Result, &item.DurationMS, &ttft, &item.PromptTokens, &item.GeneratedTokens, &item.TotalTokens, &tps, &item.QueueDurationMS, &item.LoadDurationMS, &autoloaded, &errText, &requestBody, &responseBody); err != nil {
		return RequestRecord{}, err
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

