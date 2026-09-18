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
	OwnerKind                 string     `json:"-"`
	OwnerID                   string     `json:"-"`
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
	db    database.Store
	store ObservabilityStore

	mu     sync.RWMutex
	active map[string]int
	queued map[string]int
	now    func() time.Time

	correlationMu    sync.Mutex
	correlationReady bool

	pendingLimits func(context.Context) (perInstance, global int)
}

func New(db database.Store) *Service {
	return &Service{db: db, store: NewObservabilityStore(db), active: map[string]int{}, queued: map[string]int{}, now: time.Now}
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

func (s *Service) SetPendingLimits(getter func(context.Context) (perInstance, global int)) {
	s.pendingLimits = getter
}

func (s *Service) PendingLimits(ctx context.Context) (perInstance, global int) {
	if s.pendingLimits != nil {
		return s.pendingLimits(ctx)
	}
	return 32, 128
}

func (s *Service) RecordQueueLimitRejection(ctx context.Context, instanceID, scope string) error {
	if strings.TrimSpace(instanceID) == "" {
		return fmt.Errorf("instance_id is required")
	}
	if scope != "instance" && scope != "global" {
		scope = "instance"
	}
	return s.store.AddCounter(ctx, Counter{Metric: "gateway_queue_limit_rejections_total", InstanceID: instanceID, Result: scope, Value: 1})
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
	return s.store.RecordRequest(ctx, record)
}

func (s *Service) Counters(ctx context.Context) ([]Counter, error) {
	return s.store.Counters(ctx)
}

func (s *Service) Summary(ctx context.Context, sinceMS int64) (Summary, error) {
	if sinceMS <= 0 {
		sinceMS = s.now().Add(-15 * time.Minute).UnixMilli()
	}
	summary, durations, ttfts, err := s.store.Summary(ctx, sinceMS)
	if err != nil {
		return Summary{}, err
	}
	active, queued := s.Activity()
	for _, value := range active {
		summary.Active += value
	}
	for _, value := range queued {
		summary.Queued += value
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
	return s.store.ListRequests(ctx, filters)
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
	return s.store.Timeseries(ctx, metric, sinceMS, bucketSeconds)
}

func (s *Service) Prune(ctx context.Context, retentionDays int) error {
	if retentionDays <= 0 {
		retentionDays = DefaultRetentionDays
	}
	cutoff := s.now().Add(-time.Duration(retentionDays) * 24 * time.Hour).UnixMilli()
	return s.store.PruneRequests(ctx, cutoff)
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
