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

	"github.com/brantje/llamarack/backend/internal/observability"
)

const sseMetadataLineLimit = 1 << 20

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
	stats                    observability.InferenceTurnStats
}

func (u *usageValues) merge(candidate usageValues) {
	if candidate.prompt > 0 {
		u.prompt = candidate.prompt
	}
	if candidate.generated > 0 {
		u.generated = candidate.generated
	}
	if candidate.total > 0 {
		u.total = candidate.total
	}
	if candidate.promptTPS != nil {
		u.promptTPS = candidate.promptTPS
	}
	if candidate.generationTPS != nil {
		u.generationTPS = candidate.generationTPS
	}
	mergeTurnStats(&u.stats, candidate.stats)
}

func mergeTurnStats(dst *observability.InferenceTurnStats, src observability.InferenceTurnStats) {
	if src.PromptN != nil {
		dst.PromptN = src.PromptN
	}
	if src.PromptMS != nil {
		dst.PromptMS = src.PromptMS
	}
	if src.PromptPerSecond != nil {
		dst.PromptPerSecond = src.PromptPerSecond
	}
	if src.PromptPerTokenMS != nil {
		dst.PromptPerTokenMS = src.PromptPerTokenMS
	}
	if src.PredictedN != nil {
		dst.PredictedN = src.PredictedN
	}
	if src.PredictedMS != nil {
		dst.PredictedMS = src.PredictedMS
	}
	if src.PredictedPerSecond != nil {
		dst.PredictedPerSecond = src.PredictedPerSecond
	}
	if src.PredictedPerTokenMS != nil {
		dst.PredictedPerTokenMS = src.PredictedPerTokenMS
	}
	if src.CacheN != nil {
		dst.CacheN = src.CacheN
	}
	if src.DraftN != nil {
		dst.DraftN = src.DraftN
	}
	if src.DraftNAccepted != nil {
		dst.DraftNAccepted = src.DraftNAccepted
	}
	if src.FinishReason != nil {
		dst.FinishReason = src.FinishReason
	}
	if src.ToolCallCount != nil {
		dst.ToolCallCount = src.ToolCallCount
	}
}

type responseMetrics struct {
	ttftMS                        *float64
	promptTPS, generationTPS      *float64
	promptTokens, generatedTokens int64
	totalTokens                   int64
	turnStats                     observability.InferenceTurnStats
}

func calculateResponseMetrics(started, firstByte, finished time.Time, usage usageValues) responseMetrics {
	metrics := responseMetrics{
		promptTPS: usage.promptTPS, generationTPS: usage.generationTPS,
		promptTokens: usage.prompt, generatedTokens: usage.generated, totalTokens: usage.total,
		turnStats: usage.stats,
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

func addFinalMetricHeaders(header http.Header, kind metricKind, metrics responseMetrics) {
	if kind == metricNone {
		return
	}
	if metrics.ttftMS != nil {
		setProductHeader(header, headerTTFTMS, metricFloat(*metrics.ttftMS))
	}
	if metrics.promptTPS != nil {
		setProductHeader(header, headerPromptTPS, metricFloat(*metrics.promptTPS))
	}
	if kind == metricGeneration && metrics.generationTPS != nil {
		setProductHeader(header, headerGenerationTPS, metricFloat(*metrics.generationTPS))
	}
	if metrics.promptTokens > 0 {
		setProductHeader(header, headerPromptTokens, strconv.FormatInt(metrics.promptTokens, 10))
	}
	if kind == metricGeneration && metrics.generatedTokens > 0 {
		setProductHeader(header, headerGeneratedTokens, strconv.FormatInt(metrics.generatedTokens, 10))
	}
	if metrics.totalTokens > 0 {
		setProductHeader(header, headerTotalTokens, strconv.FormatInt(metrics.totalTokens, 10))
	}
}

type usageAccumulator struct {
	values    usageValues
	toolCalls map[string]struct{}
}

func newUsageAccumulator() *usageAccumulator {
	return &usageAccumulator{toolCalls: map[string]struct{}{}}
}

func (a *usageAccumulator) addRaw(raw []byte) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return
	}
	a.addObject(value)
}

func (a *usageAccumulator) addObject(value map[string]any) {
	a.values.merge(usageFromObject(value))
	a.captureChoiceMetadata(value)
	if response, ok := value["response"].(map[string]any); ok {
		a.values.merge(usageFromObject(response))
		a.captureChoiceMetadata(response)
	}
}

func (a *usageAccumulator) captureChoiceMetadata(value map[string]any) {
	choices, _ := value["choices"].([]any)
	for choiceIndex, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		if choice == nil {
			continue
		}
		if reason, ok := choice["finish_reason"].(string); ok && strings.TrimSpace(reason) != "" {
			reason = strings.TrimSpace(reason)
			a.values.stats.FinishReason = &reason
		}
		for _, field := range []string{"message", "delta"} {
			part, _ := choice[field].(map[string]any)
			if part == nil {
				continue
			}
			tools, _ := part["tool_calls"].([]any)
			for position, rawTool := range tools {
				tool, _ := rawTool.(map[string]any)
				if tool == nil {
					continue
				}
				key := toolCallKey(choiceIndex, position, tool)
				a.toolCalls[key] = struct{}{}
			}
		}
	}
}

func toolCallKey(choiceIndex, position int, tool map[string]any) string {
	prefix := strconv.Itoa(choiceIndex) + ":"
	if index, ok := numberValue(tool["index"]); ok {
		return prefix + "index:" + strconv.FormatInt(int64(index), 10)
	}
	if id, ok := tool["id"].(string); ok && strings.TrimSpace(id) != "" {
		return prefix + "id:" + strings.TrimSpace(id)
	}
	// A tool-call delta without id/index is uncommon. Position is stable for the
	// same single-delta list and avoids inflating the count on repeated chunks.
	return prefix + "position:" + strconv.Itoa(position)
}

func (a *usageAccumulator) result() usageValues {
	result := a.values
	if len(a.toolCalls) > 0 {
		count := int64(len(a.toolCalls))
		result.stats.ToolCallCount = &count
	}
	return result
}

func parseUsage(body []byte) usageValues {
	accumulator := newUsageAccumulator()
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		accumulator.addRaw(trimmed)
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		processSSEUsageLine(accumulator, line)
	}
	return accumulator.result()
}

func processSSEUsageLine(accumulator *usageAccumulator, line []byte) {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
		return
	}
	accumulator.addRaw(line)
}

type usageStreamCollector struct {
	accumulator *usageAccumulator
	pending     []byte
	discardLine bool
	finished    bool
}

func newUsageStreamCollector() *usageStreamCollector {
	return &usageStreamCollector{accumulator: newUsageAccumulator()}
}

func (c *usageStreamCollector) Feed(data []byte) {
	for len(data) > 0 {
		if c.discardLine {
			index := bytes.IndexByte(data, '\n')
			if index < 0 {
				return
			}
			c.discardLine = false
			data = data[index+1:]
			continue
		}
		index := bytes.IndexByte(data, '\n')
		if index < 0 {
			if len(c.pending)+len(data) > sseMetadataLineLimit {
				c.pending = nil
				c.discardLine = true
				return
			}
			c.pending = append(c.pending, data...)
			return
		}
		segment := data[:index]
		if len(c.pending)+len(segment) <= sseMetadataLineLimit {
			line := make([]byte, 0, len(c.pending)+len(segment))
			line = append(line, c.pending...)
			line = append(line, segment...)
			processSSEUsageLine(c.accumulator, line)
		}
		c.pending = nil
		data = data[index+1:]
	}
}

func (c *usageStreamCollector) Finish() {
	if c.finished {
		return
	}
	c.finished = true
	if !c.discardLine && len(c.pending) > 0 {
		processSSEUsageLine(c.accumulator, c.pending)
	}
	c.pending = nil
}

func (c *usageStreamCollector) Result() usageValues {
	c.Finish()
	return c.accumulator.result()
}

type usageCaptureStream struct {
	io.ReadCloser
	collector *usageStreamCollector
}

func (s *usageCaptureStream) Read(p []byte) (int, error) {
	n, err := s.ReadCloser.Read(p)
	if n > 0 {
		s.collector.Feed(p[:n])
	}
	if err != nil {
		s.collector.Finish()
	}
	return n, err
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
		result.stats.PromptN = int64Pointer(timings, "prompt_n")
		result.stats.PromptMS = float64Pointer(timings, "prompt_ms")
		result.stats.PromptPerSecond = float64Pointer(timings, "prompt_per_second")
		result.stats.PromptPerTokenMS = float64Pointer(timings, "prompt_per_token_ms")
		result.stats.PredictedN = int64Pointer(timings, "predicted_n")
		result.stats.PredictedMS = float64Pointer(timings, "predicted_ms")
		result.stats.PredictedPerSecond = float64Pointer(timings, "predicted_per_second")
		result.stats.PredictedPerTokenMS = float64Pointer(timings, "predicted_per_token_ms")
		result.stats.CacheN = int64Pointer(timings, "cache_n")
		result.stats.DraftN = int64Pointer(timings, "draft_n")
		result.stats.DraftNAccepted = int64Pointer(timings, "draft_n_accepted")

		if result.prompt == 0 && result.stats.PromptN != nil {
			result.prompt = *result.stats.PromptN
		}
		if result.generated == 0 && result.stats.PredictedN != nil {
			result.generated = *result.stats.PredictedN
		}
		if result.total == 0 {
			result.total = result.prompt + result.generated
		}
		if result.stats.PromptPerSecond != nil && *result.stats.PromptPerSecond > 0 {
			result.promptTPS = result.stats.PromptPerSecond
		} else if result.stats.PromptMS != nil && *result.stats.PromptMS > 0 && result.prompt > 0 {
			value := float64(result.prompt) / (*result.stats.PromptMS / 1000)
			result.promptTPS = &value
		}
		if result.stats.PredictedPerSecond != nil && *result.stats.PredictedPerSecond > 0 {
			result.generationTPS = result.stats.PredictedPerSecond
		} else if result.stats.PredictedMS != nil && *result.stats.PredictedMS > 0 && result.generated > 0 {
			value := float64(result.generated) / (*result.stats.PredictedMS / 1000)
			result.generationTPS = &value
		}
		if result.stats.PromptPerTokenMS == nil && result.stats.PromptMS != nil && result.stats.PromptN != nil && *result.stats.PromptN > 0 {
			value := *result.stats.PromptMS / float64(*result.stats.PromptN)
			result.stats.PromptPerTokenMS = &value
		}
		if result.stats.PredictedPerTokenMS == nil && result.stats.PredictedMS != nil && result.stats.PredictedN != nil && *result.stats.PredictedN > 0 {
			value := *result.stats.PredictedMS / float64(*result.stats.PredictedN)
			result.stats.PredictedPerTokenMS = &value
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

func int64Pointer(values map[string]any, key string) *int64 {
	value, ok := numberValue(values[key])
	if !ok {
		return nil
	}
	converted := int64(value)
	return &converted
}

func float64Pointer(values map[string]any, key string) *float64 {
	value, ok := numberValue(values[key])
	if !ok {
		return nil
	}
	return &value
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
		return "lr_" + hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("lr_%x_%x", time.Now().UnixNano(), requestIDFallback.Add(1))
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
