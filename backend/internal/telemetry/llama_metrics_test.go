package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const fullLlamaMetricsFixture = `# HELP llamacpp:prompt_tokens_total Number of prompt tokens processed.
llamacpp:prompt_tokens_total 120
llamacpp:prompt_seconds_total 4
llamacpp:prompt_tokens_seconds 30
llamacpp:tokens_predicted_total 210
llamacpp:tokens_predicted_seconds_total 7
llamacpp:predicted_tokens_seconds 30
llamacpp:requests_processing 2
llamacpp:requests_deferred 1
llamacpp:n_tokens_max 8192
llamacpp:n_decode_total 70
llamacpp:n_busy_slots_per_decode 1.5
llamacpp:spec_decode_num_draft_tokens_total 100
llamacpp:spec_decode_num_accepted_tokens_total 75
llamacpp:spec_decode_num_drafts_total 20
llamacpp:spec_decode_num_accepted_tokens_per_pos_total{position="0"} 20
llamacpp:spec_decode_num_accepted_tokens_per_pos_total{position="1"} 18
`

func TestParseLlamaMetricsIncludesAllCurrentServerMetrics(t *testing.T) {
	metrics, ok := ParseLlamaMetrics([]byte(fullLlamaMetricsFixture))
	if !ok {
		t.Fatal("expected recognized metrics")
	}
	checks := map[string]*float64{
		"prompt tokens": metrics.PromptTokensTotal,
		"prompt seconds": metrics.PromptSecondsTotal,
		"prompt throughput": metrics.PromptTokensPerSecond,
		"predicted tokens": metrics.PredictedTokensTotal,
		"predicted seconds": metrics.PredictedSecondsTotal,
		"predicted throughput": metrics.PredictedTokensPerSecond,
		"processing": metrics.RequestsProcessing,
		"deferred": metrics.RequestsDeferred,
		"context max": metrics.ContextTokensMax,
		"decode total": metrics.DecodeTotal,
		"busy slots": metrics.BusySlotsPerDecode,
		"draft tokens": metrics.SpecDraftTokensTotal,
		"accepted tokens": metrics.SpecAcceptedTokensTotal,
		"drafts": metrics.SpecDraftsTotal,
		"acceptance": metrics.SpecAcceptanceRatePct,
	}
	for name, value := range checks {
		if value == nil {
			t.Fatalf("%s missing", name)
		}
	}
	if got := *metrics.SpecAcceptanceRatePct; got != 75 {
		t.Fatalf("acceptance=%v", got)
	}
	if got := metrics.SpecAcceptedTokensPerPosition["1"]; got != 18 {
		t.Fatalf("position 1=%v", got)
	}
}

func TestParseLlamaMetricsIgnoresUnknownMalformedAndNonFiniteValues(t *testing.T) {
	metrics, ok := ParseLlamaMetrics([]byte("unknown 9\nllamacpp:prompt_tokens_total nope\nllamacpp:prompt_seconds_total NaN\n"))
	if ok || metrics.PromptTokensTotal != nil || metrics.PromptSecondsTotal != nil {
		t.Fatalf("metrics=%+v ok=%v", metrics, ok)
	}

	metrics, ok = ParseLlamaMetrics([]byte("llamacpp:spec_decode_num_draft_tokens_total 0\nllamacpp:spec_decode_num_accepted_tokens_total 0\nllamacpp:spec_decode_num_accepted_tokens_per_pos_total 3\n"))
	if !ok {
		t.Fatal("zero speculative counters are still valid metrics")
	}
	if metrics.SpecAcceptanceRatePct != nil {
		t.Fatalf("zero draft tokens must not derive a rate: %+v", metrics)
	}
	if metrics.SpecAcceptedTokensPerPosition != nil {
		t.Fatalf("position metric without position label must be ignored: %+v", metrics)
	}
}

func TestFetchLlamaMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, fullLlamaMetricsFixture)
	}))
	defer server.Close()
	metrics, err := FetchLlamaMetrics(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.PredictedTokensPerSecond == nil || *metrics.PredictedTokensPerSecond != 30 {
		t.Fatalf("metrics=%+v", metrics)
	}
}

func TestFetchLlamaMetricsRejectsBadStatusAndEmptyBody(t *testing.T) {
	for _, handler := range []http.Handler{
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "disabled", http.StatusNotFound) }),
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, "# no metrics\n") }),
	} {
		server := httptest.NewServer(handler)
		if _, err := FetchLlamaMetrics(context.Background(), server.URL); err == nil {
			server.Close()
			t.Fatal("expected metrics fetch error")
		}
		server.Close()
	}
}
