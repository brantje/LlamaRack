package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
	"github.com/brantje/llamacpp-manager/backend/internal/telemetry"
)

func TestAttachLlamaMetricsAddsReadyMatchingRuntimeSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "llamacpp:predicted_tokens_seconds 52.9\nllamacpp:requests_processing 1\n")
	}))
	defer server.Close()

	samples := []telemetry.Sample{{InstanceID: "ready", PID: 42, CollectedAt: time.Unix(1, 0).UTC()}}
	runtimes := []supervisor.Runtime{{InstanceID: "ready", State: supervisor.Ready, PID: 42}}
	result := attachLlamaMetrics(context.Background(), samples, runtimes, func(id string) (string, bool) {
		if id != "ready" {
			t.Fatalf("id=%q", id)
		}
		return server.URL, true
	})
	if len(result) != 1 || result[0].LlamaMetrics == nil || result[0].LlamaMetrics.PredictedTokensPerSecond == nil {
		t.Fatalf("result=%+v", result)
	}
	if got := *result[0].LlamaMetrics.PredictedTokensPerSecond; got != 52.9 {
		t.Fatalf("predicted tok/s=%v", got)
	}
}

func TestAttachLlamaMetricsSkipsUnavailableAndStaleRuntimes(t *testing.T) {
	calls := 0
	resolve := func(string) (string, bool) {
		calls++
		return "http://unused", true
	}
	samples := []telemetry.Sample{
		{InstanceID: "loading", PID: 1},
		{InstanceID: "stale", PID: 2},
		{InstanceID: "unknown", PID: 3},
	}
	runtimes := []supervisor.Runtime{
		{InstanceID: "loading", State: supervisor.Loading, PID: 1},
		{InstanceID: "stale", State: supervisor.Ready, PID: 99},
	}
	result := attachLlamaMetrics(context.Background(), samples, runtimes, resolve)
	if calls != 0 {
		t.Fatalf("resolver calls=%d", calls)
	}
	for _, sample := range result {
		if sample.LlamaMetrics != nil {
			t.Fatalf("unexpected metrics: %+v", sample)
		}
	}
}

func TestAttachLlamaMetricsLeavesSnapshotUsableWhenScrapeFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "metrics disabled", http.StatusNotFound)
	}))
	defer server.Close()
	samples := []telemetry.Sample{{InstanceID: "ready", PID: 7}}
	runtimes := []supervisor.Runtime{{InstanceID: "ready", State: supervisor.Ready, PID: 7}}
	result := attachLlamaMetrics(context.Background(), samples, runtimes, func(string) (string, bool) { return server.URL, true })
	if len(result) != 1 || result[0].InstanceID != "ready" || result[0].LlamaMetrics != nil {
		t.Fatalf("result=%+v", result)
	}
}
