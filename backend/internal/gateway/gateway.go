package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/observability"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

const (
	metadataResponseCaptureLimit = 8 << 20
	preAuthRequestBodyBytes       = 64 << 10
	maxRequestBodyBytes           = 32 << 20
)

const (
	headerRequestID       = "X-LlamaCPP-Manager-Request-ID"
	headerTraceID         = "X-LiteLLM-Trace-ID"
	headerInstance        = "X-LlamaCPP-Manager-Instance"
	headerAutoloaded      = "X-LlamaCPP-Manager-Autoloaded"
	headerQueueMS         = "X-LlamaCPP-Manager-Queue-MS"
	headerLoadMS          = "X-LlamaCPP-Manager-Load-MS"
	headerTTFTMS          = "X-LlamaCPP-Manager-TTFT-MS"
	headerPromptTPS       = "X-LlamaCPP-Manager-Prompt-Tokens-Per-Second"
	headerGenerationTPS   = "X-LlamaCPP-Manager-Generation-Tokens-Per-Second"
	headerPromptTokens    = "X-LlamaCPP-Manager-Prompt-Tokens"
	headerGeneratedTokens = "X-LlamaCPP-Manager-Generated-Tokens"
	headerTotalTokens     = "X-LlamaCPP-Manager-Total-Tokens"
)

var requestIDFallback atomic.Uint64

type Gateway struct {
	auth          *auth.Service
	lifecycle     *lifecycle.Service
	observability *observability.Service
}

type requestEnvelope struct {
	Model           string `json:"model"`
	Stream          bool   `json:"stream"`
	LiteLLMMetadata struct {
		TraceID string `json:"trace_id"`
	} `json:"litellm_metadata"`
}

func New(a *auth.Service, _ *models.Service, l *lifecycle.Service, services ...*observability.Service) *Gateway {
	g := &Gateway{auth: a, lifecycle: l}
	if len(services) > 0 {
		g.observability = services[0]
		if g.observability != nil {
			if err := g.observability.EnsureCorrelationSchema(context.Background()); err != nil {
				slog.Warn("initialize inference request correlation schema failed", "error", err)
			}
		}
	}
	return g
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := newRequestID()
	w.Header().Set(headerRequestID, requestID)

	// Read only a small metadata budget before authentication. This preserves
	// useful body-derived metadata for normal/small failed-auth requests without
	// allowing an unauthenticated caller to allocate the full request-body limit.
	var prefix []byte
	var bodyReadErr error
	if r.Body != nil {
		prefix, bodyReadErr = io.ReadAll(io.LimitReader(r.Body, preAuthRequestBodyBytes))
	}
	var envelope requestEnvelope
	parseErr := error(nil)
	if len(bytes.TrimSpace(prefix)) > 0 {
		parseErr = json.Unmarshal(prefix, &envelope)
	}
	traceID := resolveTraceID(r, envelope.LiteLLMMetadata.TraceID)
	w.Header().Set(headerTraceID, traceID)

	record := observability.RequestRecord{
		StartedAt:  started.UnixMilli(),
		InstanceID: strings.TrimSpace(envelope.Model),
		Endpoint:   r.URL.Path,
		TraceID:    traceID,
		CallType:   callType(r.URL.Path),
		ClientIP:   clientIP(r),
		UserAgent:  boundedMetadata(r.UserAgent(), 2048),
		Streaming:  envelope.Stream,
	}
	observed := newResponseObserver(w, false)
	g.begin(r.Context(), requestID, record)

	var promptTPS *float64
	var proxyPanic any
	defer func() {
		if recovered := recover(); recovered != nil {
			proxyPanic = recovered
		}
		finished := time.Now()
		if record.FinishedAt == 0 {
			record.FinishedAt = finished.UnixMilli()
		}
		if record.DurationMS == 0 {
			record.DurationMS = milliseconds(finished.Sub(started))
		}
		if record.StatusCode == 0 {
			record.StatusCode = observed.StatusCode()
		}
		if proxyPanic != nil {
			record.StatusCode = http.StatusInternalServerError
			record.Result = "error"
			if record.Error == "" {
				record.Error = "Proxy stream aborted"
			}
		}
		if ctxErr := r.Context().Err(); ctxErr != nil {
			record.Result = "error"
			if record.Error == "" {
				record.Error = sanitizeError("client disconnected: " + ctxErr.Error())
			}
		}
		if record.Result == "" {
			if record.StatusCode >= 200 && record.StatusCode < 400 {
				record.Result = "success"
			} else {
				record.Result = "error"
			}
		}
		if record.Result == "error" && record.Error == "" {
			record.Error = responseError(record.StatusCode, observed.Bytes())
		}
		if observed.captureAll && record.ResponseBody == nil {
			value := string(observed.Bytes())
			record.ResponseBody = &value
		}
		g.finalize(r.Context(), requestID, promptTPS, record)
		if proxyPanic != nil {
			panic(proxyPanic)
		}
	}()

	key, err := g.authenticateKey(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		writeError(observed, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "Invalid API key")
		return
	}
	record.APIKey = &observability.APIKeyRef{ID: key.ID, Name: key.Name, Prefix: key.Prefix}

	// Authentication succeeded. Read the remainder up to one byte beyond the
	// normal limit so oversized bodies are rejected instead of truncated.
	body := append([]byte(nil), prefix...)
	bodyTooLarge := false
	if bodyReadErr == nil && r.Body != nil {
		remaining := int64(maxRequestBodyBytes + 1 - len(body))
		if remaining > 0 {
			rest, readErr := io.ReadAll(io.LimitReader(r.Body, remaining))
			body = append(body, rest...)
			bodyReadErr = readErr
		}
	}
	if len(body) > maxRequestBodyBytes {
		bodyTooLarge = true
		body = nil
	}
	if bodyReadErr == nil && !bodyTooLarge {
		var fullEnvelope requestEnvelope
		parseErr = nil
		if len(bytes.TrimSpace(body)) > 0 {
			parseErr = json.Unmarshal(body, &fullEnvelope)
		}
		envelope = fullEnvelope
		record.InstanceID = strings.TrimSpace(envelope.Model)
		record.Streaming = envelope.Stream
		if suppliedTraceID, ok := suppliedTraceID(r, envelope.LiteLLMMetadata.TraceID); ok && suppliedTraceID != traceID {
			traceID = suppliedTraceID
			record.TraceID = suppliedTraceID
			w.Header().Set(headerTraceID, suppliedTraceID)
		}
	}
	g.update(r.Context(), requestID, record)

	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		g.listModels(observed, r)
		return
	}
	if r.Method != http.MethodPost || !supported(r.URL.Path) {
		writeError(observed, http.StatusNotFound, "invalid_request_error", "not_found", "Unknown OpenAI-compatible endpoint")
		return
	}
	if bodyReadErr != nil {
		writeError(observed, http.StatusBadRequest, "invalid_request_error", "invalid_body", "Invalid request body")
		return
	}
	if bodyTooLarge {
		writeError(observed, http.StatusRequestEntityTooLarge, "invalid_request_error", "body_too_large", "Request body is too large")
		return
	}
	if parseErr != nil || strings.TrimSpace(envelope.Model) == "" {
		writeError(observed, http.StatusBadRequest, "invalid_request_error", "model_required", "A model ID is required")
		return
	}

	instance, err := g.lifecycle.Instances().Get(r.Context(), record.InstanceID)
	if err != nil {
		record.Error = sanitizeError(err.Error())
		writeError(observed, http.StatusServiceUnavailable, "server_error", "model_unavailable", err.Error())
		return
	}
	record.InstanceID = instance.ID
	w.Header().Set(headerInstance, instance.ID)
	if instance.RequestLogMode == "full" {
		value := string(body)
		record.RequestBody = &value
		observed.captureAll = true
	}

	preRuntime, _ := g.lifecycle.RuntimeInstance(r.Context(), instance.ID)
	record.Autoloaded = preRuntime.State != supervisor.Ready
	w.Header().Set(headerAutoloaded, strconv.FormatBool(record.Autoloaded))
	// Store the canonical Instance and opt-in request body before worker
	// acquisition so acquire/autoload failures still have useful detail.
	g.update(r.Context(), requestID, record)

	if g.observability != nil {
		g.observability.Queue(instance.ID)
	}
	queueStarted := time.Now()
	endpoint, release, err := g.lifecycle.Acquire(r.Context(), instance.ID)
	record.QueueDurationMS = milliseconds(time.Since(queueStarted))
	w.Header().Set(headerQueueMS, metricFloat(record.QueueDurationMS))
	if record.Autoloaded {
		record.LoadDurationMS = record.QueueDurationMS
		w.Header().Set(headerLoadMS, metricFloat(record.LoadDurationMS))
	}
	if err != nil {
		if g.observability != nil {
			g.observability.EndQueued(instance.ID)
		}
		record.Error = sanitizeError(err.Error())
		writeError(observed, http.StatusServiceUnavailable, "server_error", "model_unavailable", err.Error())
		return
	}
	defer release()

	target, err := url.Parse(endpoint)
	if err != nil {
		if g.observability != nil {
			g.observability.EndQueued(instance.ID)
		}
		record.Error = "Invalid worker endpoint"
		writeError(observed, http.StatusInternalServerError, "server_error", "invalid_worker_endpoint", "Invalid worker endpoint")
		return
	}
	if g.observability != nil {
		g.observability.Activate(instance.ID)
	}
	active := true
	defer func() {
		if active && g.observability != nil {
			g.observability.EndActive(instance.ID)
		}
	}()

	workerStarted := time.Now()
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Authorization")

	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(req *http.Request) {
		original(req)
		req.Host = target.Host
		req.Header.Del("Authorization")
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, proxyErr error) {
		slog.Warn("gateway worker proxy failed", "instance_id", instance.ID, "request_id", requestID, "error", proxyErr)
		record.Error = sanitizeError(proxyErr.Error())
		writeError(writer, http.StatusServiceUnavailable, "server_error", "backend_unavailable", "Model worker unavailable")
	}

	var completed *responseMetrics
	if !envelope.Stream {
		proxy.ModifyResponse = func(resp *http.Response) error {
			tracked := &firstReadCloser{ReadCloser: resp.Body}
			payload, readErr := io.ReadAll(tracked)
			closeErr := resp.Body.Close()
			if readErr != nil {
				return readErr
			}
			if closeErr != nil {
				return closeErr
			}
			resp.Body = io.NopCloser(bytes.NewReader(payload))
			resp.ContentLength = int64(len(payload))
			resp.Header.Set("Content-Length", strconv.Itoa(len(payload)))
			metrics := calculateResponseMetrics(workerStarted, tracked.firstRead, time.Now(), parseUsage(payload))
			completed = &metrics
			addFinalMetricHeaders(resp.Header, r.URL.Path, metrics)
			return nil
		}
	}

	func() {
		defer func() {
			proxyPanic = recover()
		}()
		proxy.ServeHTTP(observed, r)
	}()
	finished := time.Now()
	active = false
	if g.observability != nil {
		g.observability.EndActive(instance.ID)
	}

	record.StatusCode = observed.StatusCode()
	if record.StatusCode >= 200 && record.StatusCode < 400 {
		record.Result = "success"
	} else {
		record.Result = "error"
	}
	record.FinishedAt = finished.UnixMilli()
	record.DurationMS = milliseconds(finished.Sub(started))
	responseSample := observed.Bytes()
	metrics := responseMetrics{}
	if completed != nil {
		metrics = *completed
	} else {
		metrics = calculateResponseMetrics(workerStarted, observed.FirstByte(), finished, parseUsage(responseSample))
	}
	record.TTFTMS = metrics.ttftMS
	record.PromptTokens = metrics.promptTokens
	record.GeneratedTokens = metrics.generatedTokens
	record.TotalTokens = metrics.totalTokens
	record.TokensPerSecond = metrics.generationTPS
	promptTPS = metrics.promptTPS
	if record.Result == "error" && record.Error == "" {
		record.Error = responseError(record.StatusCode, responseSample)
	}
	if observed.captureAll {
		value := string(responseSample)
		record.ResponseBody = &value
	}
	if proxyPanic != nil {
		panic(proxyPanic)
	}
}

func (g *Gateway) persistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func (g *Gateway) begin(ctx context.Context, requestID string, record observability.RequestRecord) {
	if g.observability == nil {
		return
	}
	persistCtx, cancel := g.persistenceContext(ctx)
	defer cancel()
	if err := g.observability.BeginCorrelatedRequest(persistCtx, requestID, record); err != nil {
		slog.Warn("begin inference observability failed", "request_id", requestID, "instance_id", record.InstanceID, "endpoint", record.Endpoint, "error", err)
	}
}

func (g *Gateway) update(ctx context.Context, requestID string, record observability.RequestRecord) {
	if g.observability == nil {
		return
	}
	persistCtx, cancel := g.persistenceContext(ctx)
	defer cancel()
	if err := g.observability.UpdateCorrelatedRequest(persistCtx, requestID, record); err != nil {
		slog.Warn("update inference observability failed", "request_id", requestID, "instance_id", record.InstanceID, "endpoint", record.Endpoint, "error", err)
	}
}

func (g *Gateway) finalize(ctx context.Context, requestID string, promptTPS *float64, record observability.RequestRecord) {
	if g.observability == nil {
		return
	}
	persistCtx, cancel := g.persistenceContext(ctx)
	defer cancel()
	if err := g.observability.FinalizeCorrelatedRequest(persistCtx, requestID, promptTPS, record); err != nil {
		slog.Warn("finalize inference observability failed", "request_id", requestID, "instance_id", record.InstanceID, "endpoint", record.Endpoint, "error", err)
	}
}

// persist preserves the completion-only helper used by focused tests and older
// internal call sites. Finalization recovers atomically if the early row is absent.
func (g *Gateway) persist(ctx context.Context, requestID string, promptTPS *float64, record observability.RequestRecord) {
	g.finalize(ctx, requestID, promptTPS, record)
}

func (g *Gateway) authenticate(ctx context.Context, header string) error {
	_, err := g.authenticateKey(ctx, header)
	return err
}

func (g *Gateway) authenticateKey(ctx context.Context, header string) (auth.APIKey, error) {
	if !strings.HasPrefix(header, "Bearer ") {
		return auth.APIKey{}, errors.New("missing bearer token")
	}
	return g.auth.AuthenticateAPIKeyInfo(ctx, strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
}

func (g *Gateway) listModels(w http.ResponseWriter, r *http.Request) {
	items, err := g.lifecycle.Instances().List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "database_error", "Unable to list models")
		return
	}
	type item struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	out := make([]item, 0, len(items))
	for _, instance := range items {
		if instance.Enabled {
			out = append(out, item{ID: instance.ID, Object: "model", Created: time.Now().Unix(), OwnedBy: "llamacpp-manager"})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": out})
}

func supported(path string) bool {
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/embeddings":
		return true
	}
	return false
}

func callType(path string) string {
	switch path {
	case "/v1/chat/completions":
		return "chat_completion"
	case "/v1/completions":
		return "completion"
	case "/v1/responses":
		return "response"
	case "/v1/embeddings":
		return "embedding"
	default:
		return ""
	}
}

func suppliedTraceID(r *http.Request, bodyTraceID string) (string, bool) {
	for _, value := range []string{r.Header.Get(headerTraceID), bodyTraceID} {
		if traceID, ok := normalizeUUID(value); ok {
			return traceID, true
		}
	}
	return "", false
}

func resolveTraceID(r *http.Request, bodyTraceID string) string {
	if traceID, ok := suppliedTraceID(r, bodyTraceID); ok {
		return traceID
	}
	return newTraceID()
}

func normalizeUUID(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", false
	}
	for index, r := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return "", false
		}
	}
	return value, true
}

func newTraceID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		fallback := fmt.Sprintf("%032x", uint64(time.Now().UnixNano())^requestIDFallback.Add(1))
		return fmt.Sprintf("%s-%s-%s-%s-%s", fallback[0:8], fallback[8:12], fallback[12:16], fallback[16:20], fallback[20:32])
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(value[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexValue[0:8], hexValue[8:12], hexValue[12:16], hexValue[16:20], hexValue[20:32])
}

func clientIP(r *http.Request) string {
	if value := forwardedClientIP(r.Header.Values("Forwarded")); value != "" {
		return value
	}
	for _, part := range strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",") {
		if value := canonicalIP(part); value != "" {
			return value
		}
	}
	if value := canonicalIP(r.Header.Get("X-Real-IP")); value != "" {
		return value
	}
	return canonicalIP(r.RemoteAddr)
}

func forwardedClientIP(values []string) string {
	raw := strings.Join(values, ",")
	for _, element := range strings.Split(raw, ",") {
		for _, parameter := range strings.Split(element, ";") {
			key, value, ok := strings.Cut(parameter, "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "for") {
				continue
			}
			if ip := canonicalIP(strings.Trim(strings.TrimSpace(value), `"`)); ip != "" {
				return ip
			}
		}
	}
	return ""
}

func canonicalIP(value string) string {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if value == "" || strings.EqualFold(value, "unknown") || strings.HasPrefix(value, "_") {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return ""
}

func boundedMetadata(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit > 0 && len(value) > limit {
		value = value[:limit]
	}
	return value
}

type responseObserver struct {
	http.ResponseWriter
	status     int
	firstByte  time.Time
	body       bytes.Buffer
	captureAll bool
}

func newResponseObserver(writer http.ResponseWriter, captureAll bool) *responseObserver {
	return &responseObserver{ResponseWriter: writer, captureAll: captureAll}
}
func (w *responseObserver) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseObserver) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseObserver) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.firstByte.IsZero() {
		w.firstByte = time.Now()
	}
	if w.captureAll {
		_, _ = w.body.Write(value)
	} else if w.body.Len() < metadataResponseCaptureLimit {
		remaining := metadataResponseCaptureLimit - w.body.Len()
		if remaining > len(value) {
			remaining = len(value)
		}
		_, _ = w.body.Write(value[:remaining])
	}
	return w.ResponseWriter.Write(value)
}
func (w *responseObserver) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
func (w *responseObserver) StatusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
func (w *responseObserver) FirstByte() time.Time { return w.firstByte }
func (w *responseObserver) Bytes() []byte        { return append([]byte(nil), w.body.Bytes()...) }

type firstReadCloser struct {
	io.ReadCloser
	firstRead time.Time
}

func (r *firstReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.firstRead.IsZero() {
		r.firstRead = time.Now()
	}
	return n, err
}

type usageValues struct {
	prompt, generated, total int64
	promptTPS, generationTPS *float64
}

type responseMetrics struct {
	ttftMS                        *float64
	promptTPS, generationTPS      *float64
	promptTokens, generatedTokens int64
	totalTokens                   int64
}

func calculateResponseMetrics(started, firstByte, finished time.Time, usage usageValues) responseMetrics {
	metrics := responseMetrics{
		promptTPS:       usage.promptTPS,
		generationTPS:   usage.generationTPS,
		promptTokens:    usage.prompt,
		generatedTokens: usage.generated,
		totalTokens:     usage.total,
	}
	if !firstByte.IsZero() {
		value := milliseconds(firstByte.Sub(started))
		metrics.ttftMS = &value
	}
	if metrics.generationTPS == nil && usage.generated > 0 {
		generationStarted := started
		if !firstByte.IsZero() {
			generationStarted = firstByte
		}
		seconds := finished.Sub(generationStarted).Seconds()
		if seconds > 0 {
			value := float64(usage.generated) / seconds
			metrics.generationTPS = &value
		}
	}
	return metrics
}

func addFinalMetricHeaders(header http.Header, endpoint string, metrics responseMetrics) {
	if metrics.ttftMS != nil {
		header.Set(headerTTFTMS, metricFloat(*metrics.ttftMS))
	}
	if metrics.promptTPS != nil {
		header.Set(headerPromptTPS, metricFloat(*metrics.promptTPS))
	}
	if endpoint != "/v1/embeddings" && metrics.generationTPS != nil {
		header.Set(headerGenerationTPS, metricFloat(*metrics.generationTPS))
	}
	if metrics.promptTokens > 0 {
		header.Set(headerPromptTokens, strconv.FormatInt(metrics.promptTokens, 10))
	}
	if endpoint != "/v1/embeddings" && metrics.generatedTokens > 0 {
		header.Set(headerGeneratedTokens, strconv.FormatInt(metrics.generatedTokens, 10))
	}
	if metrics.totalTokens > 0 {
		header.Set(headerTotalTokens, strconv.FormatInt(metrics.totalTokens, 10))
	}
}

func parseUsage(body []byte) usageValues {
	var best usageValues
	parseObject := func(raw []byte) {
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil {
			return
		}
		candidate := usageFromObject(value)
		if candidate.total > 0 || candidate.prompt > 0 || candidate.generated > 0 || candidate.promptTPS != nil || candidate.generationTPS != nil {
			best = candidate
		}
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		parseObject(trimmed)
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(line, []byte("[DONE]")) {
			continue
		}
		parseObject(line)
	}
	return best
}

func usageFromObject(value map[string]any) usageValues {
	var result usageValues
	if raw, ok := value["usage"].(map[string]any); ok {
		result.prompt = intValue(raw, "prompt_tokens", "input_tokens")
		result.generated = intValue(raw, "completion_tokens", "output_tokens")
		result.total = intValue(raw, "total_tokens")
		if result.total == 0 {
			result.total = result.prompt + result.generated
		}
	}
	if timings, ok := value["timings"].(map[string]any); ok {
		if result.prompt == 0 {
			result.prompt = intValue(timings, "prompt_n")
		}
		if result.generated == 0 {
			result.generated = intValue(timings, "predicted_n")
		}
		if result.total == 0 {
			result.total = result.prompt + result.generated
		}
		if value, ok := numberValue(timings["prompt_per_second"]); ok && value > 0 {
			result.promptTPS = &value
		} else if promptMS, ok := numberValue(timings["prompt_ms"]); ok && promptMS > 0 && result.prompt > 0 {
			value := float64(result.prompt) / (promptMS / 1000)
			result.promptTPS = &value
		}
		if value, ok := numberValue(timings["predicted_per_second"]); ok && value > 0 {
			result.generationTPS = &value
		} else if predictedMS, ok := numberValue(timings["predicted_ms"]); ok && predictedMS > 0 && result.generated > 0 {
			value := float64(result.generated) / (predictedMS / 1000)
			result.generationTPS = &value
		}
	}
	return result
}

func intValue(values map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := numberValue(values[key]); ok {
			return int64(value)
		}
	}
	return 0
}
func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		value, err := typed.Float64()
		return value, err == nil
	default:
		return 0, false
	}
}

func newRequestID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return "lcm_" + hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("lcm_%x_%x", time.Now().UnixNano(), requestIDFallback.Add(1))
}

func milliseconds(value time.Duration) float64 { return float64(value.Microseconds()) / 1000 }
func metricFloat(value float64) string         { return strconv.FormatFloat(value, 'f', 3, 64) }

func responseError(status int, body []byte) string {
	var value map[string]any
	if json.Unmarshal(body, &value) == nil {
		if errorValue, ok := value["error"].(map[string]any); ok {
			if message, ok := errorValue["message"].(string); ok {
				return sanitizeError(message)
			}
		}
	}
	return fmt.Sprintf("HTTP %d", status)
}

func sanitizeError(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 32 && r != '\t' && r != '\n' {
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 512 {
		value = value[:512]
	}
	return value
}

func writeError(w http.ResponseWriter, status int, typ, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": typ, "param": nil, "code": code}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
