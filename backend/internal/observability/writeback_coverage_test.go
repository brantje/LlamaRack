package observability

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWritebackCloneHelpersDeepCopy(t *testing.T) {
	ttft := 1.25
	tps := 2.5
	promptTPS := 3.75
	generationTPS := 4.5
	requestBody := "request"
	responseBody := "response"
	record := RequestRecord{
		APIKey:                    &APIKeyRef{ID: "key", Name: "name", Prefix: "sk-prefix"},
		TTFTMS:                    &ttft,
		TokensPerSecond:           &tps,
		PromptTokensPerSecond:     &promptTPS,
		GenerationTokensPerSecond: &generationTPS,
		RequestBody:               &requestBody,
		ResponseBody:              &responseBody,
	}

	cloned := cloneRequestRecord(record)
	record.APIKey.Name = "mutated"
	*record.TTFTMS = 11
	*record.TokensPerSecond = 12
	*record.PromptTokensPerSecond = 13
	*record.GenerationTokensPerSecond = 14
	*record.RequestBody = "mutated request"
	*record.ResponseBody = "mutated response"

	if cloned.APIKey == record.APIKey || cloned.APIKey.Name != "name" {
		t.Fatalf("api key was not deeply cloned: %+v", cloned.APIKey)
	}
	if cloned.TTFTMS == record.TTFTMS || *cloned.TTFTMS != 1.25 {
		t.Fatalf("ttft clone=%v", cloned.TTFTMS)
	}
	if cloned.TokensPerSecond == record.TokensPerSecond || *cloned.TokensPerSecond != 2.5 {
		t.Fatalf("tps clone=%v", cloned.TokensPerSecond)
	}
	if cloned.PromptTokensPerSecond == record.PromptTokensPerSecond || *cloned.PromptTokensPerSecond != 3.75 {
		t.Fatalf("prompt tps clone=%v", cloned.PromptTokensPerSecond)
	}
	if cloned.GenerationTokensPerSecond == record.GenerationTokensPerSecond || *cloned.GenerationTokensPerSecond != 4.5 {
		t.Fatalf("generation tps clone=%v", cloned.GenerationTokensPerSecond)
	}
	if cloned.RequestBody == record.RequestBody || *cloned.RequestBody != "request" {
		t.Fatalf("request body clone=%v", cloned.RequestBody)
	}
	if cloned.ResponseBody == record.ResponseBody || *cloned.ResponseBody != "response" {
		t.Fatalf("response body clone=%v", cloned.ResponseBody)
	}
	if cloneFloat64(nil) != nil {
		t.Fatal("nil float clone should remain nil")
	}
	if cloneString(nil) != nil {
		t.Fatal("nil string clone should remain nil")
	}
}

func TestWritebackPublicLifecycleAndBufferEdges(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if s.writebackEnabled() {
		t.Fatal("writeback should be disabled until explicitly started")
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("disabled flush: %v", err)
	}
	if handled, err := s.bufferBegin("disabled", RequestRecord{}); handled || err != nil {
		t.Fatalf("disabled begin handled=%v err=%v", handled, err)
	}

	s.StartWriteback(ctx)
	if !s.writebackEnabled() || !s.WritebackEnabled() {
		t.Fatal("StartWriteback should enable buffering")
	}
	// Starting twice must be idempotent and must not create a second flusher.
	s.StartWriteback(ctx)

	record := RequestRecord{StartedAt: 1, Endpoint: "/v1/test"}
	if handled, err := s.bufferBegin("req-one", record); !handled || err != nil {
		t.Fatalf("begin handled=%v err=%v", handled, err)
	}
	if handled, err := s.bufferBegin("req-one", record); !handled || err == nil {
		t.Fatalf("duplicate begin handled=%v err=%v", handled, err)
	}
	if handled, err := s.bufferUpdate("missing", record); handled || err != nil {
		t.Fatalf("missing update handled=%v err=%v", handled, err)
	}
	if handled, err := s.bufferModelSlug("missing", "model"); handled || err != nil {
		t.Fatalf("missing model slug handled=%v err=%v", handled, err)
	}
	if handled, err := s.bufferOpenAIResponseID("missing", "resp_missing"); handled || err != nil {
		t.Fatalf("missing response id handled=%v err=%v", handled, err)
	}
	if handled, err := s.bufferRequestLogContext("missing", "session", "instance"); handled || err != nil {
		t.Fatalf("missing context handled=%v err=%v", handled, err)
	}
	if _, ok := s.bufferedRequestModelIdentity("missing"); ok {
		t.Fatal("missing buffered identity should not resolve")
	}
	if _, ok := s.bufferedStoredOpenAIResponse("resp_missing"); ok {
		t.Fatal("missing buffered response should not resolve")
	}
	if handled, err := s.bufferMarkOpenAIResponseDeleted("resp_missing"); handled || err != nil {
		t.Fatalf("missing delete handled=%v err=%v", handled, err)
	}

	state := writebackStateFor(s)
	state.mu.Lock()
	state.limit = 1
	state.mu.Unlock()
	if handled, err := s.bufferFinalize("req-two", nil, record); !handled || !errors.Is(err, ErrWritebackOverflow) {
		t.Fatalf("finalize overflow handled=%v err=%v", handled, err)
	}
}

func TestWritebackPersistsRichContextAndResponseState(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWriteback(ctx, time.Hour)

	if _, err := s.db.ExecContext(ctx, `INSERT INTO models(id,slug,name,gguf_path,total_bytes) VALUES('model-rich','model-rich','Rich Model','/tmp/rich.gguf',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO instances(id,slug,model_id,name) VALUES('instance-rich','public-rich','model-rich','Rich Instance')`); err != nil {
		t.Fatal(err)
	}

	ttft := 1.5
	tps := 10.5
	promptTPSMetric := 20.5
	generationTPS := 30.5
	promptCorrelationTPS := 40.5
	requestBody := `{"model":"public-rich","input":"hello"}`
	responseBody := `{"id":"resp_rich","status":"completed"}`
	record := RequestRecord{
		StartedAt: 1000, FinishedAt: 1100, InstanceID: "instance-rich", Endpoint: "/v1/responses",
		APIKey: &APIKeyRef{ID: "key-rich", Name: "Rich Key", Prefix: "sk-rich"}, Streaming: false,
		StatusCode: 200, Result: "success", DurationMS: 100, TTFTMS: &ttft,
		PromptTokens: 4, GeneratedTokens: 5, TotalTokens: 9, TokensPerSecond: &tps,
		PromptTokensPerSecond: &promptTPSMetric, GenerationTokensPerSecond: &generationTPS,
		RequestBody: &requestBody, ResponseBody: &responseBody,
	}
	if err := s.BeginCorrelatedRequest(ctx, "req-rich", record); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestModelSlug(ctx, "req-rich", "public-rich"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOpenAIResponseID(ctx, "req-rich", "resp_rich"); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachRequestLogContext(ctx, "req-rich", "session-rich", "public-rich"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeCorrelatedRequest(ctx, "req-rich", &promptCorrelationTPS, record); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOpenAIResponseDeleted(ctx, "resp_rich"); err != nil {
		t.Fatal(err)
	}
	var persistedBeforeFlush int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM inference_requests WHERE instance_id='instance-rich'").Scan(&persistedBeforeFlush); err != nil {
		t.Fatal(err)
	}
	if persistedBeforeFlush != 0 {
		t.Fatalf("rich writeback persisted before explicit Flush: %d", persistedBeforeFlush)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	var modelID, modelName, sessionID string
	if err := s.db.QueryRowContext(ctx, `SELECT x.model_id,x.model_name,x.session_id
		FROM inference_request_log_context x WHERE x.request_id='req-rich'`).Scan(&modelID, &modelName, &sessionID); err != nil {
		t.Fatal(err)
	}
	if modelID != "model-rich" || modelName != "Rich Model" || sessionID != "session-rich" {
		t.Fatalf("context model_id=%q model_name=%q session=%q", modelID, modelName, sessionID)
	}

	var openAIID string
	var deleted int
	if err := s.db.QueryRowContext(ctx, `SELECT openai_response_id,openai_response_deleted FROM inference_requests WHERE instance_id='instance-rich'`).Scan(&openAIID, &deleted); err != nil {
		t.Fatal(err)
	}
	if openAIID != "resp_rich" || deleted != 1 {
		t.Fatalf("response id=%q deleted=%d", openAIID, deleted)
	}

	stored, err := s.GetStoredOpenAIResponse(ctx, "resp_rich")
	if err != nil || !stored.Deleted || stored.ResponseBody == nil || *stored.ResponseBody != responseBody {
		t.Fatalf("stored response=%+v err=%v", stored, err)
	}
}
