package observability

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ManagementHandler struct{ service *Service }

type ManagementSummary struct {
	Summary
	Lifecycle LifecycleSummary `json:"lifecycle"`
	Hardware  HardwareOverview `json:"hardware"`
}

func NewManagementHandler(service *Service) http.Handler { return &ManagementHandler{service: service} }

func (h *ManagementHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/api/v1/observability/summary":
		since, err := parseSince(r, 15*time.Minute)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		value, err := h.service.Summary(r.Context(), since)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		lifecycle, err := h.service.LifecycleSummary(r.Context(), since)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, ManagementSummary{Summary: value, Lifecycle: lifecycle, Hardware: h.service.LatestHardware()})
	case "/api/v1/observability/requests":
		filters, err := parseFilters(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		limit := filters.Limit
		if limit <= 0 {
			limit = 100
		}
		queryFilters := filters
		queryFilters.Limit = limit
		if limit < 500 {
			queryFilters.Limit++
		}
		items, err := h.service.ListRequests(r.Context(), queryFilters)
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
			probe, probeErr := h.service.ListRequests(r.Context(), probeFilters)
			if probeErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": probeErr.Error()})
				return
			}
			hasMore = len(probe) > 0
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": filters.Offset, "has_more": hasMore})
	case "/api/v1/observability/timeseries":
		since, err := parseSince(r, time.Hour)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		bucket := 60
		if raw := r.URL.Query().Get("bucket_seconds"); raw != "" {
			bucket, err = strconv.Atoi(raw)
			if err != nil || bucket <= 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bucket_seconds must be a positive integer"})
				return
			}
		}
		metric := strings.TrimSpace(r.URL.Query().Get("metric"))
		instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		if isHardwareMetric(metric) {
			items, err := h.service.HardwareTimeseries(r.Context(), metric, since, bucket, strings.TrimSpace(r.URL.Query().Get("device_id")), instanceID)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"metric": metric, "bucket_seconds": bucket, "items": items})
			return
		}
		items, err := h.service.RequestTimeseries(r.Context(), metric, since, bucket, instanceID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"metric": metricOrDefault(metric), "bucket_seconds": bucket, "items": items})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func isHardwareMetric(metric string) bool {
	switch metric {
	case "ram_total_bytes", "ram_used_bytes", "vram_total_bytes", "vram_used_bytes", "gpu_utilization_pct", "instance_vram_used_bytes", "instance_cpu_percent", "instance_memory_used_bytes":
		return true
	default:
		return false
	}
}

func metricOrDefault(value string) string {
	if value == "" {
		return "requests"
	}
	return value
}

func parseSince(r *http.Request, fallback time.Duration) (int64, error) {
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return 0, fmt.Errorf("since must be a positive Unix-millisecond timestamp")
		}
		return value, nil
	}
	window := fallback
	if raw := strings.TrimSpace(r.URL.Query().Get("window_seconds")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 || seconds > 31*24*3600 {
			return 0, fmt.Errorf("window_seconds must be between 1 and %d", 31*24*3600)
		}
		window = time.Duration(seconds) * time.Second
	}
	return time.Now().Add(-window).UnixMilli(), nil
}

func parseFilters(r *http.Request) (RequestFilters, error) {
	query := r.URL.Query()
	filters := RequestFilters{
		InstanceID: strings.TrimSpace(query.Get("instance_id")),
		Endpoint:   strings.TrimSpace(query.Get("endpoint")),
		APIKeyID:   strings.TrimSpace(query.Get("api_key_id")),
		Result:     strings.TrimSpace(query.Get("result")),
		RequestID:  strings.TrimSpace(query.Get("request_id")),
		TraceID:    strings.TrimSpace(query.Get("trace_id")),
		Search:     strings.TrimSpace(query.Get("search")),
	}
	parsePositive := func(key string) (int64, error) {
		raw := strings.TrimSpace(query.Get(key))
		if raw == "" {
			return 0, nil
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer", key)
		}
		return value, nil
	}
	var err error
	if filters.SinceMS, err = parsePositive("since"); err != nil {
		return RequestFilters{}, err
	}
	if filters.BeforeMS, err = parsePositive("before"); err != nil {
		return RequestFilters{}, err
	}
	if raw := strings.TrimSpace(query.Get("status_code")); raw != "" {
		filters.StatusCode, err = strconv.Atoi(raw)
		if err != nil || filters.StatusCode < 100 || filters.StatusCode > 599 {
			return RequestFilters{}, fmt.Errorf("status_code must be between 100 and 599")
		}
	}
	if raw := strings.TrimSpace(query.Get("streaming")); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return RequestFilters{}, fmt.Errorf("streaming must be true or false")
		}
		filters.Streaming = &value
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		filters.Limit, err = strconv.Atoi(raw)
		if err != nil || filters.Limit <= 0 || filters.Limit > 500 {
			return RequestFilters{}, fmt.Errorf("limit must be between 1 and 500")
		}
	}
	if raw := strings.TrimSpace(query.Get("offset")); raw != "" {
		filters.Offset, err = strconv.Atoi(raw)
		if err != nil || filters.Offset < 0 {
			return RequestFilters{}, fmt.Errorf("offset must be zero or greater")
		}
	}
	if filters.Result != "" && filters.Result != "success" && filters.Result != "error" {
		return RequestFilters{}, fmt.Errorf("result must be success or error")
	}
	return filters, nil
}

type tokenProvider func(context.Context) string
type stateProvider func() map[string]string

type MetricsHandler struct {
	service *Service
	token   tokenProvider
	states  stateProvider
}

func NewMetricsHandler(service *Service, token tokenProvider, states ...stateProvider) http.Handler {
	var provider stateProvider
	if len(states) > 0 {
		provider = states[0]
	}
	return &MetricsHandler{service: service, token: token, states: provider}
}

func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if h.token != nil {
		expected := strings.TrimSpace(h.token(r.Context()))
		if expected != "" {
			provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
	}
	counters, err := h.service.Counters(r.Context())
	if err != nil {
		http.Error(w, "metrics unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writeMetricHelp(w, "gateway_requests_total", "OpenAI-compatible gateway requests.")
	writeMetricType(w, "gateway_requests_total", "counter")
	declaredMetric := map[string]bool{}
	for _, counter := range counters {
		if counter.Metric != "gateway_requests_total" && !declaredMetric[counter.Metric] {
			writeMetricType(w, counter.Metric, "counter")
			declaredMetric[counter.Metric] = true
		}
		var labels []string
		if counter.InstanceID != "" {
			labels = append(labels, `instance_id="`+promEscape(counter.InstanceID)+`"`)
		}
		if counter.Metric == "gateway_requests_total" || strings.Contains(counter.Metric, "tokens") {
			labels = append(labels, `endpoint="`+promEscape(counter.Endpoint)+`"`, `streaming="`+strconv.FormatBool(counter.Streaming)+`"`)
		}
		if counter.Metric == "gateway_requests_total" {
			labels = append(labels, `status_code="`+strconv.Itoa(counter.StatusCode)+`"`, `result="`+promEscape(counter.Result)+`"`)
		}
		labelText := ""
		if len(labels) > 0 {
			labelText = "{" + strings.Join(labels, ",") + "}"
		}
		writeMetricSample(w, counter.Metric, labelText, strconv.FormatFloat(counter.Value, 'f', -1, 64))
	}
	active, queued := h.service.Activity()
	writeMetricType(w, "gateway_active_requests", "gauge")
	for instanceID, value := range active {
		writeMetricSample(w, "gateway_active_requests", `{instance_id="`+promEscape(instanceID)+`"}`, strconv.Itoa(value))
	}
	writeMetricType(w, "gateway_queued_requests", "gauge")
	for instanceID, value := range queued {
		writeMetricSample(w, "gateway_queued_requests", `{instance_id="`+promEscape(instanceID)+`"}`, strconv.Itoa(value))
	}
	summary, err := h.service.Summary(r.Context(), time.Now().Add(-15*time.Minute).UnixMilli())
	if err == nil {
		writeQuantiles(w, "request_latency_seconds", summary.LatencyMS)
		writeQuantiles(w, "request_ttft_seconds", summary.TTFTMS)
	}
	writeHardwareMetrics(w, h.service.LatestHardware())
	if h.states != nil {
		writeInstanceStateMetrics(w, h.states())
	}
}

func writeHardwareMetrics(w http.ResponseWriter, overview HardwareOverview) {
	hardware := overview.Hardware
	writeMetricType(w, "ram_total_bytes", "gauge")
	writeMetricSample(w, "ram_total_bytes", "", strconv.FormatInt(hardware.RAMTotalBytes, 10))
	writeMetricType(w, "ram_used_bytes", "gauge")
	ramUsed := hardware.RAMTotalBytes - hardware.RAMAvailableBytes
	if ramUsed < 0 {
		ramUsed = 0
	}
	writeMetricSample(w, "ram_used_bytes", "", strconv.FormatInt(ramUsed, 10))
	writeMetricType(w, "gpu_vram_total_bytes", "gauge")
	writeMetricType(w, "gpu_vram_used_bytes", "gauge")
	writeMetricType(w, "gpu_utilization_percent", "gauge")
	for _, gpu := range hardware.GPUs {
		label := `{device_id="` + promEscape(gpu.ID) + `"}`
		writeMetricSample(w, "gpu_vram_total_bytes", label, strconv.FormatInt(gpu.TotalBytes, 10))
		writeMetricSample(w, "gpu_vram_used_bytes", label, strconv.FormatInt(gpu.UsedBytes, 10))
		writeMetricSample(w, "gpu_utilization_percent", label, strconv.FormatFloat(gpu.UtilizationPct, 'f', -1, 64))
	}
	writeMetricType(w, "instance_memory_used_bytes", "gauge")
	writeMetricType(w, "instance_cpu_percent", "gauge")
	writeMetricType(w, "instance_vram_used_bytes", "gauge")
	for _, sample := range overview.Telemetry {
		instance := `{instance_id="` + promEscape(sample.InstanceID) + `"}`
		if sample.MemoryUsedBytes != nil {
			writeMetricSample(w, "instance_memory_used_bytes", instance, strconv.FormatInt(*sample.MemoryUsedBytes, 10))
		}
		if sample.CPUPercent != nil {
			writeMetricSample(w, "instance_cpu_percent", instance, strconv.FormatFloat(*sample.CPUPercent, 'f', -1, 64))
		}
		if sample.VRAMUsedBytes != nil {
			writeMetricSample(w, "instance_vram_used_bytes", instance, strconv.FormatInt(*sample.VRAMUsedBytes, 10))
		}
	}
}

func writeInstanceStateMetrics(w http.ResponseWriter, states map[string]string) {
	writeMetricType(w, "instance_state", "gauge")
	instanceIDs := make([]string, 0, len(states))
	for instanceID := range states {
		instanceIDs = append(instanceIDs, instanceID)
	}
	sort.Strings(instanceIDs)
	knownStates := []string{"UNLOADED", "STARTING", "LOADING", "READY", "STOPPING", "FAILED"}
	for _, instanceID := range instanceIDs {
		current := strings.ToUpper(strings.TrimSpace(states[instanceID]))
		for _, state := range knownStates {
			value := 0
			if current == state {
				value = 1
			}
			writeMetricSample(w, "instance_state", `{instance_id="`+promEscape(instanceID)+`",state="`+state+`"}`, strconv.Itoa(value))
		}
	}
}

func writeQuantiles(w http.ResponseWriter, suffix string, values Percentiles) {
	writeMetricType(w, suffix, "gauge")
	for quantile, value := range map[string]*float64{"0.50": values.P50, "0.95": values.P95, "0.99": values.P99} {
		if value != nil {
			writeMetricSample(w, suffix, `{window="15m",quantile="`+quantile+`"}`, strconv.FormatFloat(*value/1000, 'f', -1, 64))
		}
	}
}

func promEscape(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"").Replace(value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
