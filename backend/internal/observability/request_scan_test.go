package observability

import (
	"context"
	"errors"
	"testing"
)

type failingRequestScanner struct{}

func (failingRequestScanner) Scan(...any) error { return errors.New("scan failed") }

func TestLegacyRequestScannerFullAndErrorPaths(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	ttft, tps := 12.5, 42.25
	requestBody, responseBody := `{"model":"coder"}`, `{"ok":true}`
	record := RequestRecord{
		StartedAt:       10,
		FinishedAt:      20,
		InstanceID:      "coder",
		Endpoint:        "/v1/chat/completions",
		APIKey:          &APIKeyRef{ID: "key-1", Name: "primary", Prefix: "pk_1"},
		Streaming:       true,
		StatusCode:      503,
		Result:          "error",
		DurationMS:      10,
		TTFTMS:          &ttft,
		PromptTokens:    2,
		GeneratedTokens: 3,
		TotalTokens:     5,
		TokensPerSecond: &tps,
		QueueDurationMS: 1,
		LoadDurationMS:  2,
		Autoloaded:      true,
		Error:           "worker unavailable",
		RequestBody:     &requestBody,
		ResponseBody:    &responseBody,
	}
	if err := s.RecordRequest(ctx, record); err != nil {
		t.Fatal(err)
	}
	row := s.db.QueryRowContext(ctx, `SELECT id,started_at,finished_at,instance_id,endpoint,api_key_id,api_key_name,api_key_prefix,streaming,status_code,result,duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,request_body,response_body FROM inference_requests WHERE instance_id=?`, "coder")
	got, err := scanRequest(row)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Streaming || !got.Autoloaded || got.APIKey == nil || got.APIKey.Prefix != "pk_1" || got.TTFTMS == nil || *got.TTFTMS != ttft || got.TokensPerSecond == nil || *got.TokensPerSecond != tps {
		t.Fatalf("scanner metadata=%+v", got)
	}
	if got.Error != "worker unavailable" || got.RequestBody == nil || *got.RequestBody != requestBody || got.ResponseBody == nil || *got.ResponseBody != responseBody {
		t.Fatalf("scanner retained fields=%+v", got)
	}
	if _, err := scanRequest(failingRequestScanner{}); err == nil || err.Error() != "scan failed" {
		t.Fatalf("scanner error=%v", err)
	}
}
