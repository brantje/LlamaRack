package observability

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ManagementHandler struct{ service *Service }

func NewManagementHandler(service *Service) http.Handler { return &ManagementHandler{service: service} }

func (h *ManagementHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch path {
	case "/api/v1/observability/summary":
		since, err := parseSince(r, 15*time.Minute)
		if err != nil { writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()}); return }
		value, err := h.service.Summary(r.Context(), since)
		if err != nil { writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()}); return }
		writeJSON(w, http.StatusOK, value)
	case "/api/v1/observability/requests":
		filters, err := parseFilters(r)
		if err != nil { writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()}); return }
		items, err := h.service.ListRequests(r.Context(), filters)
		if err != nil { writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()}); return }
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case "/api/v1/observability/timeseries":
		since, err := parseSince(r, time.Hour)
		if err != nil { writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()}); return }
		bucket := 60
		if raw := r.URL.Query().Get("bucket_seconds"); raw != "" {
			bucket, err = strconv.Atoi(raw)
			if err != nil || bucket <= 0 { writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bucket_seconds must be a positive integer"}); return }
		}
		metric := strings.TrimSpace(r.URL.Query().Get("metric"))
		items, err := h.service.Timeseries(r.Context(), metric, since, bucket)
		if err != nil { writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()}); return }
		writeJSON(w, http.StatusOK, map[string]any{"metric": metricOrDefault(metric), "bucket_seconds": bucket, "items": items})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func metricOrDefault(value string) string { if value == "" { return "requests" }; return value }

func parseSince(r *http.Request, fallback time.Duration) (int64, error) {
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 { return 0, fmt.Errorf("since must be a positive Unix-millisecond timestamp") }
		return value, nil
	}
	window := fallback
	if raw := strings.TrimSpace(r.URL.Query().Get("window_seconds")); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 || seconds > 31*24*3600 { return 0, fmt.Errorf("window_seconds must be between 1 and %d", 31*24*3600) }
		window = time.Duration(seconds) * time.Second
	}
	return time.Now().Add(-window).UnixMilli(), nil
}

func parseFilters(r *http.Request) (RequestFilters, error) {
	query := r.URL.Query()
	filters := RequestFilters{InstanceID: strings.TrimSpace(query.Get("instance_id")), Endpoint: strings.TrimSpace(query.Get("endpoint")), APIKeyID: strings.TrimSpace(query.Get("api_key_id")), Result: strings.TrimSpace(query.Get("result"))}
	parseInt64 := func(key string) (int64, error) {
		raw := strings.TrimSpace(query.Get(key)); if raw == "" { return 0, nil }
		value, err := strconv.ParseInt(raw, 10, 64); if err != nil || value <= 0 { return 0, fmt.Errorf("%s must be a positive integer", key) }; return value, nil
	}
	var err error
	if filters.SinceMS, err = parseInt64("since"); err != nil { return RequestFilters{}, err }
	if filters.BeforeMS, err = parseInt64("before"); err != nil { return RequestFilters{}, err }
	if raw := strings.TrimSpace(query.Get("status_code")); raw != "" {
		filters.StatusCode, err = strconv.Atoi(raw); if err != nil || filters.StatusCode < 100 || filters.StatusCode > 599 { return RequestFilters{}, fmt.Errorf("status_code must be between 100 and 599") }
	}
	if raw := strings.TrimSpace(query.Get("streaming")); raw != "" {
		value, parseErr := strconv.ParseBool(raw); if parseErr != nil { return RequestFilters{}, fmt.Errorf("streaming must be true or false") }; filters.Streaming = &value
	}
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		filters.Limit, err = strconv.Atoi(raw); if err != nil || filters.Limit <= 0 || filters.Limit > 500 { return RequestFilters{}, fmt.Errorf("limit must be between 1 and 500") }
	}
	return filters, nil
}

type tokenProvider func(context.Context) string

type MetricsHandler struct {
	service *Service
	token   tokenProvider
}

func NewMetricsHandler(service *Service, token tokenProvider) http.Handler { return &MetricsHandler{service: service, token: token} }

func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet { w.WriteHeader(http.StatusMethodNotAllowed); return }
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
	if err != nil { http.Error(w, "metrics unavailable", http.StatusInternalServerError); return }
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintln(w, "# HELP llamacpp_manager_gateway_requests_total OpenAI-compatible gateway requests.")
	fmt.Fprintln(w, "# TYPE llamacpp_manager_gateway_requests_total counter")
	declaredTokenMetric := map[string]bool{}
	for _, counter := range counters {
		name := "llamacpp_manager_" + counter.Metric
		if counter.Metric != "gateway_requests_total" && !declaredTokenMetric[counter.Metric] {
			fmt.Fprintf(w, "# TYPE %s counter\n", name)
			declaredTokenMetric[counter.Metric] = true
		}
		labels := []string{`instance_id="`+promEscape(counter.InstanceID)+`"`, `endpoint="`+promEscape(counter.Endpoint)+`"`, `streaming="`+strconv.FormatBool(counter.Streaming)+`"`}
		if counter.Metric == "gateway_requests_total" {
			labels = append(labels, `status_code="`+strconv.Itoa(counter.StatusCode)+`"`, `result="`+promEscape(counter.Result)+`"`)
		}
		fmt.Fprintf(w, "%s{%s} %s\n", name, strings.Join(labels, ","), strconv.FormatFloat(counter.Value, 'f', -1, 64))
	}
	active, queued := h.service.Activity()
	fmt.Fprintln(w, "# TYPE llamacpp_manager_gateway_active_requests gauge")
	for instanceID, value := range active { fmt.Fprintf(w, "llamacpp_manager_gateway_active_requests{instance_id=\"%s\"} %d\n", promEscape(instanceID), value) }
	fmt.Fprintln(w, "# TYPE llamacpp_manager_gateway_queued_requests gauge")
	for instanceID, value := range queued { fmt.Fprintf(w, "llamacpp_manager_gateway_queued_requests{instance_id=\"%s\"} %d\n", promEscape(instanceID), value) }
	summary, err := h.service.Summary(r.Context(), time.Now().Add(-15*time.Minute).UnixMilli())
	if err == nil {
		writeQuantiles(w, "llamacpp_manager_request_latency_seconds", summary.LatencyMS)
		writeQuantiles(w, "llamacpp_manager_request_ttft_seconds", summary.TTFTMS)
	}
}

func writeQuantiles(w http.ResponseWriter, name string, values Percentiles) {
	fmt.Fprintf(w, "# TYPE %s gauge\n", name)
	for quantile, value := range map[string]*float64{"0.50": values.P50, "0.95": values.P95, "0.99": values.P99} {
		if value != nil { fmt.Fprintf(w, "%s{window=\"15m\",quantile=\"%s\"} %s\n", name, quantile, strconv.FormatFloat(*value/1000, 'f', -1, 64)) }
	}
}

func promEscape(value string) string { return strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"").Replace(value) }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
