package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/observability"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

const metadataResponseCaptureLimit = 8 << 20

type Gateway struct {
	auth          *auth.Service
	lifecycle     *lifecycle.Service
	observability *observability.Service
}

func New(a *auth.Service, _ *models.Service, l *lifecycle.Service, services ...*observability.Service) *Gateway {
	g := &Gateway{auth: a, lifecycle: l}
	if len(services) > 0 { g.observability = services[0] }
	return g
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key, err := g.authenticateKey(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "Invalid API key")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		g.listModels(w, r)
		return
	}
	if r.Method != http.MethodPost || !supported(r.URL.Path) {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Unknown OpenAI-compatible endpoint")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_body", "Invalid request body")
		return
	}
	var envelope struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || strings.TrimSpace(envelope.Model) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model_required", "A model ID is required")
		return
	}
	instanceID := strings.TrimSpace(envelope.Model)
	instance, err := g.lifecycle.Instances().Get(r.Context(), instanceID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "server_error", "model_unavailable", err.Error())
		return
	}

	started := time.Now()
	record := observability.RequestRecord{
		StartedAt: started.UnixMilli(), InstanceID: instance.ID, Endpoint: r.URL.Path, Streaming: envelope.Stream,
		APIKey: &observability.APIKeyRef{ID: key.ID, Name: key.Name, Prefix: key.Prefix},
	}
	if instance.RequestLogMode == "full" { value := string(body); record.RequestBody = &value }

	preRuntime, _ := g.lifecycle.RuntimeInstance(r.Context(), instance.ID)
	record.Autoloaded = preRuntime.State != supervisor.Ready
	if g.observability != nil { g.observability.Queue(instance.ID) }
	queueStarted := time.Now()
	endpoint, release, err := g.lifecycle.Acquire(r.Context(), instance.ID)
	record.QueueDurationMS = float64(time.Since(queueStarted).Microseconds()) / 1000
	if record.Autoloaded { record.LoadDurationMS = record.QueueDurationMS }
	if err != nil {
		if g.observability != nil { g.observability.EndQueued(instance.ID) }
		record.StatusCode = http.StatusServiceUnavailable
		record.Result = "error"
		record.Error = sanitizeError(err.Error())
		record.FinishedAt = time.Now().UnixMilli()
		record.DurationMS = float64(time.Since(started).Microseconds()) / 1000
		g.persist(r.Context(), record)
		writeError(w, http.StatusServiceUnavailable, "server_error", "model_unavailable", err.Error())
		return
	}
	defer release()
	if g.observability != nil { g.observability.Activate(instance.ID) }
	active := true
	defer func() {
		if active && g.observability != nil { g.observability.EndActive(instance.ID) }
	}()

	target, err := url.Parse(endpoint)
	if err != nil {
		record.StatusCode = http.StatusInternalServerError
		record.Result = "error"
		record.Error = "Invalid worker endpoint"
		record.FinishedAt = time.Now().UnixMilli()
		record.DurationMS = float64(time.Since(started).Microseconds()) / 1000
		g.persist(r.Context(), record)
		writeError(w, http.StatusInternalServerError, "server_error", "invalid_worker_endpoint", "Invalid worker endpoint")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Authorization")

	captureAll := instance.RequestLogMode == "full"
	observed := newResponseObserver(w, captureAll)
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(req *http.Request) {
		original(req)
		req.Host = target.Host
		req.Header.Del("Authorization")
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, proxyErr error) {
		slog.Warn("gateway worker proxy failed", "instance_id", instance.ID, "error", proxyErr)
		writeError(writer, http.StatusServiceUnavailable, "server_error", "backend_unavailable", "Model worker unavailable")
	}
	proxy.ServeHTTP(observed, r)
	finished := time.Now()
	active = false
	if g.observability != nil { g.observability.EndActive(instance.ID) }

	record.StatusCode = observed.StatusCode()
	if record.StatusCode >= 200 && record.StatusCode < 400 { record.Result = "success" } else { record.Result = "error" }
	record.FinishedAt = finished.UnixMilli()
	record.DurationMS = float64(finished.Sub(started).Microseconds()) / 1000
	if !observed.FirstByte().IsZero() { value := float64(observed.FirstByte().Sub(started).Microseconds()) / 1000; record.TTFTMS = &value }
	responseSample := observed.Bytes()
	usage := parseUsage(responseSample)
	record.PromptTokens, record.GeneratedTokens, record.TotalTokens = usage.prompt, usage.generated, usage.total
	if usage.tps != nil { record.TokensPerSecond = usage.tps } else if usage.generated > 0 {
		generationDuration := finished.Sub(started).Seconds()
		if record.TTFTMS != nil { generationDuration -= *record.TTFTMS / 1000 }
		if generationDuration > 0 { value := float64(usage.generated) / generationDuration; record.TokensPerSecond = &value }
	}
	if record.Result == "error" { record.Error = responseError(record.StatusCode, responseSample) }
	if captureAll { value := string(responseSample); record.ResponseBody = &value }
	g.persist(r.Context(), record)
}

func (g *Gateway) persist(ctx context.Context, record observability.RequestRecord) {
	if g.observability == nil { return }
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := g.observability.RecordRequest(persistCtx, record); err != nil {
		slog.Warn("persist inference observability failed", "instance_id", record.InstanceID, "endpoint", record.Endpoint, "error", err)
	}
}

func (g *Gateway) authenticate(ctx context.Context, header string) error {
	_, err := g.authenticateKey(ctx, header)
	return err
}

func (g *Gateway) authenticateKey(ctx context.Context, header string) (auth.APIKey, error) {
	if !strings.HasPrefix(header, "Bearer ") { return auth.APIKey{}, errors.New("missing bearer token") }
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
		if instance.Enabled { out = append(out, item{ID: instance.ID, Object: "model", Created: time.Now().Unix(), OwnedBy: "llamacpp-manager"}) }
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": out})
}

func supported(path string) bool {
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/embeddings": return true
	}
	return false
}

type responseObserver struct {
	http.ResponseWriter
	status     int
	firstByte  time.Time
	body       bytes.Buffer
	captureAll bool
}

func newResponseObserver(writer http.ResponseWriter, captureAll bool) *responseObserver { return &responseObserver{ResponseWriter: writer, captureAll: captureAll} }
func (w *responseObserver) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseObserver) WriteHeader(status int) { if w.status == 0 { w.status = status }; w.ResponseWriter.WriteHeader(status) }
func (w *responseObserver) Write(value []byte) (int, error) {
	if w.status == 0 { w.status = http.StatusOK }
	if w.firstByte.IsZero() { w.firstByte = time.Now() }
	if w.captureAll { _, _ = w.body.Write(value) } else if w.body.Len() < metadataResponseCaptureLimit {
		remaining := metadataResponseCaptureLimit - w.body.Len()
		if remaining > len(value) { remaining = len(value) }
		_, _ = w.body.Write(value[:remaining])
	}
	return w.ResponseWriter.Write(value)
}
func (w *responseObserver) Flush() { if flusher, ok := w.ResponseWriter.(http.Flusher); ok { flusher.Flush() } }
func (w *responseObserver) StatusCode() int { if w.status == 0 { return http.StatusOK }; return w.status }
func (w *responseObserver) FirstByte() time.Time { return w.firstByte }
func (w *responseObserver) Bytes() []byte { return append([]byte(nil), w.body.Bytes()...) }

type usageValues struct { prompt, generated, total int64; tps *float64 }

func parseUsage(body []byte) usageValues {
	var best usageValues
	parseObject := func(raw []byte) {
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil { return }
		candidate := usageFromObject(value)
		if candidate.total > 0 || candidate.prompt > 0 || candidate.generated > 0 || candidate.tps != nil { best = candidate }
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' { parseObject(trimmed) }
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) { continue }
		line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(line, []byte("[DONE]")) { continue }
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
		if result.total == 0 { result.total = result.prompt + result.generated }
	}
	if timings, ok := value["timings"].(map[string]any); ok {
		if result.prompt == 0 { result.prompt = intValue(timings, "prompt_n") }
		if result.generated == 0 { result.generated = intValue(timings, "predicted_n") }
		if result.total == 0 { result.total = result.prompt + result.generated }
		if value, ok := numberValue(timings["predicted_per_second"]); ok { result.tps = &value }
	}
	return result
}

func intValue(values map[string]any, keys ...string) int64 {
	for _, key := range keys { if value, ok := numberValue(values[key]); ok { return int64(value) } }
	return 0
}
func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64: return typed, true
	case json.Number: value, err := typed.Float64(); return value, err == nil
	default: return 0, false
	}
}

func responseError(status int, body []byte) string {
	var value map[string]any
	if json.Unmarshal(body, &value) == nil {
		if errorValue, ok := value["error"].(map[string]any); ok {
			if message, ok := errorValue["message"].(string); ok { return sanitizeError(message) }
		}
	}
	return fmt.Sprintf("HTTP %d", status)
}

func sanitizeError(value string) string {
	value = strings.Map(func(r rune) rune { if r < 32 && r != '\t' && r != '\n' { return -1 }; return r }, value)
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 512 { value = value[:512] }
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
