package observability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestWritebackBuffersUntilContextReadyAndFlushes(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWriteback(ctx, time.Hour)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO models(id,slug,name,gguf_path,total_bytes) VALUES('model-a','model-a','Model A','model.gguf',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO instances(id,slug,model_id,name) VALUES('instance-a','public-model','model-a','Instance A')`); err != nil {
		t.Fatal(err)
	}

	record := RequestRecord{StartedAt: 1000, InstanceID: "instance-a", Endpoint: "/v1/chat/completions", StatusCode: 200}
	if err := s.BeginCorrelatedRequest(ctx, "req-a", record); err != nil {
		t.Fatal(err)
	}
	record.APIKey = &APIKeyRef{ID: "key-a", Name: "Key A", Prefix: "sk-lr-a"}
	if err := s.UpdateCorrelatedRequest(ctx, "req-a", record); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestModelSlug(ctx, "req-a", "public-model"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeCorrelatedRequest(ctx, "req-a", nil, record); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM inference_requests").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("writeback persisted before flush: %d", count)
	}
	if flushed, err := s.flushWriteback(ctx, true); err != nil || flushed != 0 {
		t.Fatalf("ready flush before context=%d err=%v", flushed, err)
	}
	if err := s.AttachRequestLogContext(ctx, "req-a", "session-a", "instance-a"); err != nil {
		t.Fatal(err)
	}
	if flushed, err := s.flushWriteback(ctx, true); err != nil || flushed != 1 {
		t.Fatalf("ready flush=%d err=%v", flushed, err)
	}

	var requestID, instanceID, modelSlug, sessionID, modelID, modelName string
	if err := s.db.QueryRowContext(ctx, `SELECT c.request_id,r.instance_id,r.model_slug,x.session_id,x.model_id,x.model_name
        FROM inference_requests r
        JOIN inference_request_correlations c ON c.inference_request_id=r.id
        JOIN inference_request_log_context x ON x.request_id=c.request_id`).Scan(&requestID, &instanceID, &modelSlug, &sessionID, &modelID, &modelName); err != nil {
		t.Fatal(err)
	}
	if requestID != "req-a" || instanceID != "instance-a" || modelSlug != "public-model" || sessionID != "session-a" || modelID != "model-a" || modelName != "Model A" {
		t.Fatalf("persisted request=%q instance=%q slug=%q session=%q model=%q name=%q", requestID, instanceID, modelSlug, sessionID, modelID, modelName)
	}
}

func TestWritebackExplicitFlushPersistsFinalizedWithoutContext(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWriteback(ctx, time.Hour)

	record := RequestRecord{StartedAt: 1000, InstanceID: "instance-a", Endpoint: "/v1/embeddings", StatusCode: 200}
	if err := s.BeginCorrelatedRequest(ctx, "req-explicit", record); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeCorrelatedRequest(ctx, "req-explicit", nil, record); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM inference_requests").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("explicit flush count=%d", count)
	}
}

func TestWritebackRecordCorrelatedIsImmediatelyReady(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWriteback(ctx, time.Hour)

	record := RequestRecord{StartedAt: 1000, InstanceID: "instance-a", Endpoint: "/v1/chat/completions", StatusCode: 200}
	if err := s.RecordCorrelatedRequest(ctx, "req-complete", nil, record); err != nil {
		t.Fatal(err)
	}
	if flushed, err := s.flushWriteback(ctx, true); err != nil || flushed != 1 {
		t.Fatalf("completion-only flush=%d err=%v", flushed, err)
	}
}

func TestWritebackOverflowFailsWithoutBlocking(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWriteback(ctx, time.Hour)
	state := writebackStateFor(s)
	state.mu.Lock()
	state.limit = 1
	state.mu.Unlock()

	record := RequestRecord{StartedAt: 1000, Endpoint: "/v1/test"}
	if err := s.BeginCorrelatedRequest(ctx, "req-one", record); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginCorrelatedRequest(ctx, "req-two", record); !errors.Is(err, ErrWritebackOverflow) {
		t.Fatalf("overflow err=%v", err)
	}
}

func TestWritebackFailureRequeuesBatch(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWriteback(ctx, time.Hour)

	record := RequestRecord{StartedAt: 1000, InstanceID: "instance-a", Endpoint: "/v1/test", StatusCode: 200}
	if err := s.RecordCorrelatedRequest(ctx, "req-requeue", nil, record); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.flushWriteback(ctx, true); err == nil {
		t.Fatal("expected flush failure")
	}
	state := writebackStateFor(s)
	state.mu.Lock()
	_, exists := state.entries["req-requeue"]
	state.mu.Unlock()
	if !exists {
		t.Fatal("failed batch was not requeued")
	}
}

func TestWritebackBuffersStoredResponseState(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWriteback(ctx, time.Hour)

	requestBody := `{"model":"public-model","input":"hello"}`
	responseBody := `{"id":"resp_buffered","status":"completed"}`
	record := RequestRecord{
		StartedAt: 1000, InstanceID: "instance-a", Endpoint: "/v1/responses", StatusCode: 200,
		RequestBody: &requestBody, ResponseBody: &responseBody,
	}
	if err := s.BeginCorrelatedRequest(ctx, "req-response", record); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOpenAIResponseID(ctx, "req-response", "resp_buffered"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRequestModelSlug(ctx, "req-response", "public-model"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeCorrelatedRequest(ctx, "req-response", nil, record); err != nil {
		t.Fatal(err)
	}

	stored, err := s.GetStoredOpenAIResponse(ctx, "resp_buffered")
	if err != nil || stored.ResponseBody == nil || *stored.ResponseBody != responseBody {
		t.Fatalf("buffered stored response=%+v err=%v", stored, err)
	}
	identity, err := s.RequestModelIdentity(ctx, "req-response")
	if err != nil || identity.InstanceID != "instance-a" || identity.ModelSlug != "public-model" {
		t.Fatalf("buffered identity=%+v err=%v", identity, err)
	}
	if err := s.MarkOpenAIResponseDeleted(ctx, "resp_buffered"); err != nil {
		t.Fatal(err)
	}
	stored, err = s.GetStoredOpenAIResponse(ctx, "resp_buffered")
	if err != nil || !stored.Deleted {
		t.Fatalf("deleted buffered response=%+v err=%v", stored, err)
	}
	if err := s.MarkOpenAIResponseDeleted(ctx, "resp_buffered"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second delete err=%v", err)
	}
}

func TestWritebackPersistenceIsIdempotentByRequestID(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	s.startWriteback(ctx, time.Hour)
	record := RequestRecord{StartedAt: 1000, InstanceID: "instance-a", Endpoint: "/v1/test", StatusCode: 200}
	if err := s.RecordCorrelatedRequest(ctx, "req-idempotent", nil, record); err != nil {
		t.Fatal(err)
	}
	if _, err := s.flushWriteback(ctx, true); err != nil {
		t.Fatal(err)
	}
	if handled, err := s.bufferFinalize("req-idempotent", nil, record); !handled || err != nil {
		t.Fatalf("rebuffer handled=%v err=%v", handled, err)
	}
	if handled, err := s.bufferRequestLogContext("req-idempotent", "session", ""); !handled || err != nil {
		t.Fatalf("context handled=%v err=%v", handled, err)
	}
	if _, err := s.flushWriteback(ctx, true); err != nil {
		t.Fatal(err)
	}
	var requests, correlations int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM inference_requests").Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM inference_request_correlations WHERE request_id='req-idempotent'").Scan(&correlations); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || correlations != 1 {
		t.Fatalf("requests=%d correlations=%d", requests, correlations)
	}
}

func TestBufferedRecordRequiresStartedAt(t *testing.T) {
	s := testService(t)
	s.startWriteback(context.Background(), time.Hour)
	err := s.RecordCorrelatedRequest(context.Background(), "req-no-start", nil, RequestRecord{InstanceID: "instance-a", Endpoint: "/v1/test"})
	if err == nil || err.Error() != "started_at is required" {
		t.Fatalf("err=%v", err)
	}
}

func TestWritebackFlushDropsPermanentFailuresAndDrainsRemainingBatches(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	s.startWriteback(ctx, time.Hour)

	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_permanent_writeback
BEFORE INSERT ON inference_requests
WHEN NEW.endpoint='/v1/permanent'
BEGIN
	SELECT RAISE(ABORT, 'constraint failed: permanent writeback fixture');
END`); err != nil {
		t.Fatal(err)
	}

	record := RequestRecord{StartedAt: 1000, InstanceID: "instance-permanent", Endpoint: "/v1/permanent", StatusCode: 500}
	for i := 0; i < writebackBatchSize+1; i++ {
		requestID := fmt.Sprintf("req-permanent-%03d", i)
		if err := s.RecordCorrelatedRequest(ctx, requestID, nil, record); err != nil {
			t.Fatalf("buffer %s: %v", requestID, err)
		}
	}

	if err := s.Flush(ctx); err != nil {
		t.Fatalf("explicit Flush should drop permanent failures and continue draining: %v", err)
	}
	state := writebackStateFor(s)
	state.mu.Lock()
	pending := len(state.entries)
	state.mu.Unlock()
	if pending != 0 {
		t.Fatalf("explicit Flush left %d buffered entries after permanent failures", pending)
	}
}
