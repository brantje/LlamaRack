package observability

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWritebackFailedBeginDoesNotFallBackToSQLite(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWriteback(ctx, time.Hour)
	s.db.SetMaxOpenConns(1)

	state := writebackStateFor(s)
	state.mu.Lock()
	state.limit = 0
	state.mu.Unlock()

	record := RequestRecord{StartedAt: 1000, Endpoint: "/v1/chat/completions"}
	if err := s.BeginCorrelatedRequest(ctx, "req-begin-overflow", record); !errors.Is(err, ErrWritebackOverflow) {
		t.Fatalf("begin err=%v", err)
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hotCtx, hotCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer hotCancel()

	if err := s.SetRequestModelSlug(hotCtx, "req-begin-overflow", "public-model"); err != nil {
		_ = conn.Close()
		t.Fatalf("model slug fell back to SQLite: %v", err)
	}
	record.InstanceID = "instance-a"
	if err := s.UpdateCorrelatedRequest(hotCtx, "req-begin-overflow", record); err != nil {
		_ = conn.Close()
		t.Fatalf("update fell back to SQLite: %v", err)
	}
	if err := s.SetOpenAIResponseID(hotCtx, "req-begin-overflow", "resp_overflow"); err != nil {
		_ = conn.Close()
		t.Fatalf("response id fell back to SQLite: %v", err)
	}
	if err := s.AttachRequestLogContext(hotCtx, "req-begin-overflow", "session-a", "public-model"); err != nil {
		_ = conn.Close()
		t.Fatalf("request context fell back to SQLite: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	state.limit = 1
	state.mu.Unlock()
	record.StatusCode = 200
	if err := s.FinalizeCorrelatedRequest(ctx, "req-begin-overflow", nil, record); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachRequestLogContext(ctx, "req-begin-overflow", "session-a", "public-model"); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	var instanceID string
	if err := s.db.QueryRowContext(ctx, `SELECT r.instance_id
		FROM inference_requests r
		JOIN inference_request_correlations c ON c.inference_request_id=r.id
		WHERE c.request_id=?`, "req-begin-overflow").Scan(&instanceID); err != nil {
		t.Fatal(err)
	}
	if instanceID != "instance-a" {
		t.Fatalf("persisted instance=%q", instanceID)
	}
}
