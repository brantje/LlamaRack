package gateway

import (
	"bytes"
	"fmt"
	"testing"
)

func TestParseUsageCapturesCompleteLlamaTimings(t *testing.T) {
	usage := parseUsage([]byte(`{
		"usage":{"prompt_tokens":12,"completion_tokens":24,"total_tokens":36},
		"timings":{
			"prompt_n":12,"prompt_ms":6,"prompt_per_second":2000,"prompt_per_token_ms":0.5,
			"predicted_n":24,"predicted_ms":480,"predicted_per_second":50,"predicted_per_token_ms":20,
			"cache_n":8,"draft_n":10,"draft_n_accepted":7
		},
		"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"call-a"},{"id":"call-b"}]}}]
	}`))
	if usage.prompt != 12 || usage.generated != 24 || usage.total != 36 {
		t.Fatalf("usage=%+v", usage)
	}
	if usage.promptTPS == nil || *usage.promptTPS != 2000 || usage.generationTPS == nil || *usage.generationTPS != 50 {
		t.Fatalf("rates prompt=%v generation=%v", usage.promptTPS, usage.generationTPS)
	}
	stats := usage.stats
	if stats.PromptMS == nil || *stats.PromptMS != 6 || stats.PromptPerTokenMS == nil || *stats.PromptPerTokenMS != 0.5 {
		t.Fatalf("prompt stats=%+v", stats)
	}
	if stats.PredictedMS == nil || *stats.PredictedMS != 480 || stats.CacheN == nil || *stats.CacheN != 8 {
		t.Fatalf("generation stats=%+v", stats)
	}
	if stats.DraftN == nil || *stats.DraftN != 10 || stats.DraftNAccepted == nil || *stats.DraftNAccepted != 7 {
		t.Fatalf("draft stats=%+v", stats)
	}
	if stats.FinishReason == nil || *stats.FinishReason != "tool_calls" || stats.ToolCallCount == nil || *stats.ToolCallCount != 2 {
		t.Fatalf("finish/tool stats=%+v", stats)
	}
}

func TestParseUsageDerivesPerTokenTimingButNotMissingZeros(t *testing.T) {
	usage := parseUsage([]byte(`{"timings":{"prompt_n":4,"prompt_ms":2,"predicted_n":5,"predicted_ms":10,"cache_n":0}}`))
	if usage.stats.PromptPerTokenMS == nil || *usage.stats.PromptPerTokenMS != 0.5 {
		t.Fatalf("prompt ms/token=%v", usage.stats.PromptPerTokenMS)
	}
	if usage.stats.PredictedPerTokenMS == nil || *usage.stats.PredictedPerTokenMS != 2 {
		t.Fatalf("predicted ms/token=%v", usage.stats.PredictedPerTokenMS)
	}
	if usage.stats.CacheN == nil || *usage.stats.CacheN != 0 {
		t.Fatalf("authoritative cache zero lost: %+v", usage.stats)
	}
	if usage.stats.DraftN != nil || usage.stats.DraftNAccepted != nil {
		t.Fatalf("missing speculative fields must stay nil: %+v", usage.stats)
	}
}

func TestUsageStreamCollectorCapturesFinalMetadataAfterLargeStream(t *testing.T) {
	collector := newUsageStreamCollector()
	content := bytes.Repeat([]byte("x"), 4096)
	line := append([]byte(`data: {"choices":[{"delta":{"content":"`), content...)
	line = append(line, []byte(`"}}]}`+"\n\n")...)
	written := 0
	for written <= metadataResponseCaptureLimit+(1<<20) {
		collector.Feed(line)
		written += len(line)
	}
	collector.Feed([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-a\"}]}}]}\n\n"))
	collector.Feed([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0}]}}]}\n\n"))
	collector.Feed([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call-b\"}]}}]}\n\n"))
	final := []byte(`data: {"usage":{"prompt_tokens":9,"completion_tokens":11,"total_tokens":20},"timings":{"prompt_n":9,"prompt_ms":3,"predicted_n":11,"predicted_ms":220,"cache_n":5,"draft_n":4,"draft_n_accepted":3},"choices":[{"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n")
	// Deliberately split inside the final JSON object to prove the collector does
	// not depend on upstream read boundaries.
	cut := len(final) / 2
	collector.Feed(final[:cut])
	collector.Feed(final[cut:])
	usage := collector.Result()
	if usage.prompt != 9 || usage.generated != 11 || usage.total != 20 {
		t.Fatalf("usage after %d bytes=%+v", written, usage)
	}
	if usage.stats.PredictedMS == nil || *usage.stats.PredictedMS != 220 || usage.stats.CacheN == nil || *usage.stats.CacheN != 5 {
		t.Fatalf("final timings=%+v", usage.stats)
	}
	if usage.stats.FinishReason == nil || *usage.stats.FinishReason != "stop" {
		t.Fatalf("finish reason=%+v", usage.stats.FinishReason)
	}
	if usage.stats.ToolCallCount == nil || *usage.stats.ToolCallCount != 2 {
		t.Fatalf("tool calls=%+v", usage.stats.ToolCallCount)
	}
}

func TestUsageStreamCollectorRecoversAfterOversizedMetadataLine(t *testing.T) {
	collector := newUsageStreamCollector()
	collector.Feed([]byte("data: " + string(bytes.Repeat([]byte("x"), sseMetadataLineLimit+32))))
	collector.Feed([]byte("\n"))
	collector.Feed([]byte(fmt.Sprintf("data: {\"timings\":{\"predicted_n\":%d,\"predicted_ms\":12}}\n\n", 3)))
	usage := collector.Result()
	if usage.generated != 3 || usage.stats.PredictedMS == nil || *usage.stats.PredictedMS != 12 {
		t.Fatalf("collector did not recover after oversized line: %+v", usage)
	}
}
