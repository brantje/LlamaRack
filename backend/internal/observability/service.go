package observability

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const DefaultRetentionDays = 30

type APIKeyRef struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
}

type RequestRecord struct {
	ID                        int64      `json:"id"`
	RequestID                 string     `json:"request_id,omitempty"`
	TraceID                   string     `json:"trace_id,omitempty"`
	CallType                  string     `json:"call_type,omitempty"`
	StartedAt                 int64      `json:"started_at"`
	FinishedAt                int64      `json:"finished_at"`
	InstanceID                string     `json:"instance_id,omitempty"`
	Endpoint                  string     `json:"endpoint"`
	APIKey                    *APIKeyRef `json:"api_key,omitempty"`
	ClientIP                  string     `json:"client_ip,omitempty"`
	UserAgent                 string     `json:"user_agent,omitempty"`
	Streaming                 bool       `json:"streaming"`
	StatusCode                int        `json:"status_code"`
	Result                    string     `json:"result"`
	DurationMS                float64    `json:"duration_ms"`
	TTFTMS                    *float64   `json:"ttft_ms,omitempty"`
	PromptTokens              int64      `json:"prompt_tokens"`
	GeneratedTokens           int64      `json:"generated_tokens"`
	TotalTokens               int64      `json:"total_tokens"`
	TokensPerSecond           *float64   `json:"tokens_per_second,omitempty"`
	PromptTokensPerSecond     *float64   `json:"prompt_tokens_per_second,omitempty"`
	GenerationTokensPerSecond *float64   `json:"generation_tokens_per_second,omitempty"`
	QueueDurationMS           float64    `json:"queue_duration_ms"`
	LoadDurationMS            float64    `json:"load_duration_ms"`
	Autoloaded                bool       `json:"autoloaded"`
	Error                     string     `json:"error,omitempty"`
	// Request/response bodies intentionally never serialize through the shared
	// request record. The correlated detail DTO exposes them explicitly so list
	// APIs and WebSocket snapshots cannot leak full-mode content.
	RequestBody  *string `json:"-"`
	ResponseBody *string `json:"-"`
}

type Percentiles struct {
	P50 *float64 `json:"p50,omitempty"`
	P95 *float64 `json:"p95,omitempty"`
	P99 *float64 `json:"p99,omitempty"`
}

type Summary struct {
	Since           int64       `json:"since"`
	Requests        int64       `json:"requests"`
	Successes       int64       `json:"successes"`
	Errors          int64       `json:"errors"`
	Active          int         `json:"active"`
	Queued          int         `json:"queued"`
	ActiveAPIKeys   int64       `json:"active_api_keys"`
	PromptTokens    int64       `json:"prompt_tokens"`
	GeneratedTokens int64       `json:"generated_tokens"`
	TotalTokens     int64       `json:"total_tokens"`
	LatencyMS       Percentiles `json:"latency_ms"`
	TTFTMS          Percentiles `json:"ttft_ms"`
}

type RequestFilters struct {
	SinceMS    int64
	BeforeMS   int64
	InstanceID string
	Endpoint   string
	APIKeyID   string
	Result     string
	RequestID  string
	TraceID    string
	Search     string
	StatusCode int
	Streaming  *bool
	Limit      int
	Offset     int
}

type SeriesPoint struct {
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

type Counter struct {
	Metric     string
	InstanceID string
	Endpoint   string
	StatusCode int
	Result     string
	Streaming  bool
	Value      float64
}

type Service struct {
	db *sql.DB

	mu     sync.RWMutex
	active map[string]int
	queued map[string]int
	now    func() time.Time

	correlationMu    sync.Mutex
	correlationReady bool
}

func New(db *sql.DB) *Service {
	return &Service{db: db, active: map[string]int{}, queued: map[string]int{}, now: time.Now}
}

func (s *Service) Queue(instanceID string) {
	s.mu.Lock()
	s.queued[instanceID]++
	s.mu.Unlock()
}

func (s *Service) Activate(instanceID string) {
	s.mu.Lock()
	if s.queued[instanceID] > 0 {
		s.queued[instanceID]--
	}
	s.active[instanceID]++
	s.mu.Unlock()
}

func (s *Service) EndQueued(instanceID string) {
	s.mu.Lock()
	if s.queued[instanceID] > 0 {
		s.queued[instanceID]--
	}
	s.mu.Unlock()
}

func (s *Service) EndActive(instanceID string) {
	s.mu.Lock()
	if s.active[instanceID] > 0 {
		s.active[instanceID]--
	}
	s.mu.Unlock()
}

func (s *Service) Activity() (active, queued map[string]int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	active = make(map[string]int, len(s.active))
	queued = make(map[string]int, len(s.queued))
	for key, value := range s.active {
		if value > 0 {
			active[key] = value
		}
	}
	for key, value := range s.queued {
		if value > 0 {
			queued[key] = value
		}
	}
	return active, queued
}

// RecordRequest is the legacy completion-only persistence path used outside the
// gateway lifecycle. Gateway requests use the correlated Begin/Finalize path.
func (s *Service) RecordRequest(ctx context.Context, record RequestRecord) error {
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
	if record.ID == 0 {
		record.ID, _ = result.LastInsertId()
	}
	if err := addFinalCounters(ctx, tx, record); err != nil {
		return err
	}
	return tx.Commit()
}

func addFinalCounters(ctx context.Context, tx *sql.Tx, record RequestRecord) error {
	if err := addCounter(ctx, tx, Counter{Metric: "gateway_requests_total", InstanceID: record.InstanceID, Endpoint: record.Endpoint, StatusCode: record.StatusCode, Result: record.Result, Streaming: record.Streaming, Value: 1}); err != nil {
		return err
	}
	for metric, value := range map[string]int64{
		"prompt_tokens_total": record.PromptTokens, "generated_tokens_total": record.GeneratedTokens, "tokens_total": record.TotalTokens,
	} {
		if value > 0 {
			if err := addCounter(ctx, tx, Counter{Metric: metric, InstanceID: record.InstanceID, Endpoint: record.Endpoint, Streaming: record.Streaming, Value: float64(value)}); err != nil {
				return err
			}
		}
	}
	return nil
}

func addCounter(ctx context.Context, tx *sql.Tx, counter Counter) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO observability_counters(metric,instance_id,endpoint,status_code,result,streaming,value)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT(metric,instance_id,endpoint,status_code,result,streaming)
		DO UPDATE SET value=value+excluded.value`, counter.Metric, counter.InstanceID, counter.Endpoint, counter.StatusCode, counter.Result, boolInt(counter.Streaming), counter.Value)
	return err
}

func (s *Service) Counters(ctx context.Context) ([]Counter, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT metric,instance_id,endpoint,status_code,result,streaming,value FROM observability_counters ORDER BY metric,instance_id,endpoint,status_code,result,streaming`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Counter
	for rows.Next() {
		var item Counter
		var streaming int
		if err := rows.Scan(&item.Metric, &item.InstanceID, &item.Endpoint, &item.StatusCode, &item.Result, &streaming, &item.Value); err != nil {
			return nil, err
		}
		item.Streaming = streaming != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) Summary(ctx context.Context, sinceMS int64) (Summary, error) {
	if sinceMS <= 0 {
		sinceMS = s.now().Add(-15 * time.Minute).UnixMilli()
	}
	var summary Summary
	summary.Since = sinceMS
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN result='success' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN result='error' THEN 1 ELSE 0 END),0),
		COUNT(DISTINCT CASE WHEN api_key_id IS NOT NULL AND api_key_id<>'' THEN api_key_id END),
		COALESCE(SUM(prompt_tokens),0),COALESCE(SUM(generated_tokens),0),COALESCE(SUM(total_tokens),0)
		FROM inference_requests WHERE started_at>=? AND finished_at>0`, sinceMS).Scan(&summary.Requests, &summary.Successes, &summary.Errors, &summary.ActiveAPIKeys, &summary.PromptTokens, &summary.GeneratedTokens, &summary.TotalTokens); err != nil {
		return Summary{}, err
	}
	active, queued := s.Activity()
	for _, value := range active {
		summary.Active += value
	}
	for _, value := range queued {
		summary.Queued += value
	}
	rows, err := s.db.QueryContext(ctx, `SELECT duration_ms,ttft_ms FROM inference_requests WHERE started_at>=? AND finished_at>0 ORDER BY started_at`, sinceMS)
	if err != nil {
		return Summary{}, err
	}
	var durations, ttfts []float64
	for rows.Next() {
		var duration float64
		var ttft sql.NullFloat64
		if err := rows.Scan(&duration, &ttft); err != nil {
			rows.Close()
			return Summary{}, err
		}
		durations = append(durations, duration)
		if ttft.Valid {
			ttfts = append(ttfts, ttft.Float64)
		}
	}
	if err := rows.Close(); err != nil {
		return Summary{}, err
	}
	summary.LatencyMS = percentiles(durations)
	summary.TTFTMS = percentiles(ttfts)
	return summary, nil
}

func percentiles(values []float64) Percentiles {
	if len(values) == 0 {
		return Percentiles{}
	}
	values = append([]float64(nil), values...)
	sort.Float64s(values)
	pick := func(q float64) *float64 {
		index := int(math.Ceil(q*float64(len(values)))) - 1
		if index < 0 {
			index = 0
		}
		if index >= len(values) {
			index = len(values) - 1
		}
		value := values[index]
		return &value
	}
	return Percentiles{P50: pick(.50), P95: pick(.95), P99: pick(.99)}
}

func (s *Service) ListRequests(ctx context.Context, filters RequestFilters) ([]RequestRecord, error) {
	if filters.Limit <= 0 || filters.Limit > 500 {
		filters.Limit = 100
	}
	if filters.Offset < 0 {
		filters.Offset = 0
	}
	if err := s.EnsureCorrelationSchema(ctx); err != nil {
		return nil, err
	}
	query := `SELECT COALESCE(c.request_id,''),
		r.id,r.trace_id,r.call_type,r.started_at,r.finished_at,r.instance_id,r.endpoint,r.api_key_id,r.api_key_name,r.api_key_prefix,r.client_ip,r.user_agent,
		r.streaming,r.status_code,r.result,r.duration_ms,r.ttft_ms,r.prompt_tokens,r.generated_tokens,r.total_tokens,r.tokens_per_second,
		c.prompt_tokens_per_second,r.queue_duration_ms,r.load_duration_ms,r.autoloaded,r.error,NULL,NULL
		FROM inference_requests r LEFT JOIN inference_request_correlations c ON c.inference_request_id=r.id WHERE 1=1`
	var args []any
	add := func(clause string, value any) { query += clause; args = append(args, value) }
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
		query += ` AND (c.request_id LIKE ? OR r.trace_id LIKE ? OR r.instance_id LIKE ? OR r.endpoint LIKE ? OR COALESCE(r.api_key_name,'') LIKE ? OR COALESCE(r.api_key_prefix,'') LIKE ? OR COALESCE(r.error,'') LIKE ? OR r.client_ip LIKE ? OR r.user_agent LIKE ?)`
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
		return nil, err
	}
	defer rows.Close()
	var out []RequestRecord
	for rows.Next() {
		item, err := scanEnrichedRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

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

func (s *Service) Timeseries(ctx context.Context, metric string, sinceMS int64, bucketSeconds int) ([]SeriesPoint, error) {
	if sinceMS <= 0 {
		sinceMS = s.now().Add(-time.Hour).UnixMilli()
	}
	if bucketSeconds <= 0 {
		bucketSeconds = 60
	}
	if bucketSeconds > 24*3600 {
		bucketSeconds = 24 * 3600
	}
	bucketMS := int64(bucketSeconds) * 1000
	expression := "COUNT(*)"
	completedOnly := ""
	switch metric {
	case "requests", "":
		metric = "requests"
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
	query := fmt.Sprintf(`SELECT (started_at / ?) * ? AS bucket,%s FROM inference_requests WHERE started_at>=?%s GROUP BY bucket ORDER BY bucket`, expression, completedOnly)
	rows, err := s.db.QueryContext(ctx, query, bucketMS, bucketMS, sinceMS)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SeriesPoint
	for rows.Next() {
		var point SeriesPoint
		var value sql.NullFloat64
		if err := rows.Scan(&point.Timestamp, &value); err != nil {
			return nil, err
		}
		if value.Valid {
			point.Value = value.Float64
		}
		out = append(out, point)
	}
	return out, rows.Err()
}

func (s *Service) Prune(ctx context.Context, retentionDays int) error {
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	cutoff := s.now().Add(-time.Duration(retentionDays) * 24 * time.Hour).UnixMilli()
	_, err := s.db.ExecContext(ctx, `DELETE FROM inference_requests WHERE started_at<?`, cutoff)
	return err
}

func (s *Service) RunRetention(ctx context.Context, retentionDays func(context.Context) int) {
	prune := func() {
		days := DefaultRetentionDays
		if retentionDays != nil {
			if value := retentionDays(ctx); value > 0 {
				days = value
			}
		}
		_ = s.Prune(ctx, days)
	}
	prune()
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
