package gateway

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type responseObserver struct {
	http.ResponseWriter
	status     int
	firstByte  time.Time
	body       bytes.Buffer
	captureAll bool
}

func newResponseObserver(writer http.ResponseWriter, captureAll bool) *responseObserver { return &responseObserver{ResponseWriter: writer, captureAll: captureAll} }
func (w *responseObserver) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseObserver) WriteHeader(status int) {
	if w.status == 0 { w.status = status }
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseObserver) Write(value []byte) (int, error) {
	if w.status == 0 { w.status = http.StatusOK }
	if w.firstByte.IsZero() { w.firstByte = time.Now() }
	if w.captureAll {
		_, _ = w.body.Write(value)
	} else if w.body.Len() < metadataResponseCaptureLimit {
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

type firstReadCloser struct { io.ReadCloser; firstRead time.Time }
func (r *firstReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.firstRead.IsZero() { r.firstRead = time.Now() }
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
	metrics := responseMetrics{promptTPS: usage.promptTPS, generationTPS: usage.generationTPS, promptTokens: usage.prompt, generatedTokens: usage.generated, totalTokens: usage.total}
	if !firstByte.IsZero() {
		value := milliseconds(firstByte.Sub(started)); metrics.ttftMS = &value
	}
	if metrics.generationTPS == nil && usage.generated > 0 {
		generationStarted := started
		if !firstByte.IsZero() { generationStarted = firstByte }
		seconds := finished.Sub(generationStarted).Seconds()
		if seconds > 0 { value := float64(usage.generated) / seconds; metrics.generationTPS = &value }
	}
	return metrics
}

func addFinalMetricHeaders(header http.Header, kind metricKind, metrics responseMetrics) {
	if kind == metricNone { return }
	if metrics.ttftMS != nil { setProductHeader(header, headerTTFTMS, metricFloat(*metrics.ttftMS)) }
	if metrics.promptTPS != nil { setProductHeader(header, headerPromptTPS, metricFloat(*metrics.promptTPS)) }
	if kind == metricGeneration && metrics.generationTPS != nil { setProductHeader(header, headerGenerationTPS, metricFloat(*metrics.generationTPS)) }
	if metrics.promptTokens > 0 { setProductHeader(header, headerPromptTokens, strconv.FormatInt(metrics.promptTokens, 10)) }
	if kind == metricGeneration && metrics.generatedTokens > 0 { setProductHeader(header, headerGeneratedTokens, strconv.FormatInt(metrics.generatedTokens, 10)) }
	if metrics.totalTokens > 0 { setProductHeader(header, headerTotalTokens, strconv.FormatInt(metrics.totalTokens, 10)) }
}

func parseUsage(body []byte) usageValues {
	var best usageValues
	parseObject := func(raw []byte) {
		var value map[string]any
		if json.Unmarshal(raw, &value) != nil { return }
		candidate := usageFromObject(value)
		if candidate.total > 0 || candidate.prompt > 0 || candidate.generated > 0 || candidate.promptTPS != nil || candidate.generationTPS != nil { best = candidate }
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
		if value, ok := numberValue(timings["prompt_per_second"]); ok && value > 0 { result.promptTPS = &value
		} else if promptMS, ok := numberValue(timings["prompt_ms"]); ok && promptMS > 0 && result.prompt > 0 { value := float64(result.prompt) / (promptMS / 1000); result.promptTPS = &value }
		if value, ok := numberValue(timings["predicted_per_second"]); ok && value > 0 { result.generationTPS = &value
		} else if predictedMS, ok := numberValue(timings["predicted_ms"]); ok && predictedMS > 0 && result.generated > 0 { value := float64(result.generated) / (predictedMS / 1000); result.generationTPS = &value }
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

func newRequestID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil { return "lr_" + hex.EncodeToString(random[:]) }
	return fmt.Sprintf("lr_%x_%x", time.Now().UnixNano(), requestIDFallback.Add(1))
}

func milliseconds(value time.Duration) float64 { return float64(value.Microseconds()) / 1000 }
func metricFloat(value float64) string { return strconv.FormatFloat(value, 'f', 3, 64) }

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
