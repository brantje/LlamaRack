package telemetry

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// LlamaMetrics is the complete current llama.cpp server /metrics snapshot plus
// one derived speculative-decoding acceptance percentage.
type LlamaMetrics struct {
	PromptTokensTotal              *float64           `json:"prompt_tokens_total,omitempty"`
	PromptSecondsTotal             *float64           `json:"prompt_seconds_total,omitempty"`
	PromptTokensPerSecond          *float64           `json:"prompt_tokens_per_second,omitempty"`
	PredictedTokensTotal           *float64           `json:"predicted_tokens_total,omitempty"`
	PredictedSecondsTotal          *float64           `json:"predicted_seconds_total,omitempty"`
	PredictedTokensPerSecond       *float64           `json:"predicted_tokens_per_second,omitempty"`
	RequestsProcessing             *float64           `json:"requests_processing,omitempty"`
	RequestsDeferred               *float64           `json:"requests_deferred,omitempty"`
	ContextTokensMax               *float64           `json:"context_tokens_max,omitempty"`
	DecodeTotal                    *float64           `json:"decode_total,omitempty"`
	BusySlotsPerDecode             *float64           `json:"busy_slots_per_decode,omitempty"`
	SpecDraftTokensTotal           *float64           `json:"spec_draft_tokens_total,omitempty"`
	SpecAcceptedTokensTotal        *float64           `json:"spec_accepted_tokens_total,omitempty"`
	SpecDraftsTotal                *float64           `json:"spec_drafts_total,omitempty"`
	SpecAcceptedTokensPerPosition  map[string]float64 `json:"spec_accepted_tokens_per_position,omitempty"`
	SpecAcceptanceRatePct          *float64           `json:"spec_acceptance_rate_pct,omitempty"`
}

var llamaMetricsHTTPClient = &http.Client{Timeout: 750 * time.Millisecond}

func FetchLlamaMetrics(ctx context.Context, endpoint string) (*LlamaMetrics, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	response, err := llamaMetricsHTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("llama metrics returned status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	metrics, ok := ParseLlamaMetrics(body)
	if !ok {
		return nil, fmt.Errorf("llama metrics response contained no recognized metrics")
	}
	return &metrics, nil
}

func ParseLlamaMetrics(body []byte) (LlamaMetrics, bool) {
	metrics := LlamaMetrics{}
	recognized := false
	for _, rawLine := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		metricToken := fields[0]
		name := metricToken
		if brace := strings.IndexByte(name, '{'); brace >= 0 {
			name = name[:brace]
		}
		switch name {
		case "llamacpp:prompt_tokens_total":
			metrics.PromptTokensTotal = metricFloat64(value)
		case "llamacpp:prompt_seconds_total":
			metrics.PromptSecondsTotal = metricFloat64(value)
		case "llamacpp:prompt_tokens_seconds":
			metrics.PromptTokensPerSecond = metricFloat64(value)
		case "llamacpp:tokens_predicted_total":
			metrics.PredictedTokensTotal = metricFloat64(value)
		case "llamacpp:tokens_predicted_seconds_total":
			metrics.PredictedSecondsTotal = metricFloat64(value)
		case "llamacpp:predicted_tokens_seconds":
			metrics.PredictedTokensPerSecond = metricFloat64(value)
		case "llamacpp:requests_processing":
			metrics.RequestsProcessing = metricFloat64(value)
		case "llamacpp:requests_deferred":
			metrics.RequestsDeferred = metricFloat64(value)
		case "llamacpp:n_tokens_max":
			metrics.ContextTokensMax = metricFloat64(value)
		case "llamacpp:n_decode_total":
			metrics.DecodeTotal = metricFloat64(value)
		case "llamacpp:n_busy_slots_per_decode":
			metrics.BusySlotsPerDecode = metricFloat64(value)
		case "llamacpp:spec_decode_num_draft_tokens_total":
			metrics.SpecDraftTokensTotal = metricFloat64(value)
		case "llamacpp:spec_decode_num_accepted_tokens_total":
			metrics.SpecAcceptedTokensTotal = metricFloat64(value)
		case "llamacpp:spec_decode_num_drafts_total":
			metrics.SpecDraftsTotal = metricFloat64(value)
		case "llamacpp:spec_decode_num_accepted_tokens_per_pos_total":
			position, ok := prometheusLabel(metricToken, "position")
			if !ok {
				continue
			}
			if metrics.SpecAcceptedTokensPerPosition == nil {
				metrics.SpecAcceptedTokensPerPosition = map[string]float64{}
			}
			metrics.SpecAcceptedTokensPerPosition[position] = value
		default:
			continue
		}
		recognized = true
	}
	if metrics.SpecDraftTokensTotal != nil && metrics.SpecAcceptedTokensTotal != nil && *metrics.SpecDraftTokensTotal > 0 {
		metrics.SpecAcceptanceRatePct = metricFloat64(*metrics.SpecAcceptedTokensTotal / *metrics.SpecDraftTokensTotal * 100)
	}
	return metrics, recognized
}

func prometheusLabel(metricToken, key string) (string, bool) {
	marker := key + "=\""
	start := strings.Index(metricToken, marker)
	if start < 0 {
		return "", false
	}
	start += len(marker)
	end := strings.IndexByte(metricToken[start:], '"')
	if end < 0 {
		return "", false
	}
	return metricToken[start : start+end], true
}

func metricFloat64(value float64) *float64 {
	copy := value
	return &copy
}
