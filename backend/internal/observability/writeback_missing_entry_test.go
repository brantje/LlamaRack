package observability

import (
	"context"
	"testing"
	"time"
)

func TestWritebackActiveEntryRecoveryStaysOffSQLite(t *testing.T) {
	s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWriteback(ctx, time.Hour)
	s.db.SetMaxOpenConns(1)

	record := RequestRecord{StartedAt: 1000, Endpoint: "/v1/chat/completions"}
	if err := s.BeginCorrelatedRequest(ctx, "req-missing-active", record); err != nil {
		t.Fatal(err)
	}

	state := writebackStateFor(s)
	state.mu.Lock()
	original := state.entries["req-missing-active"]
	delete(state.entries, "req-missing-active")
	active := state.activeEntries["req-missing-active"]
	state.mu.Unlock()
	if original == nil || active != original {
		t.Fatal("begin did not retain the active entry snapshot")
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}

	hotCtx, hotCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	if err := s.SetRequestModelSlug(hotCtx, "req-missing-active", "public-model"); err != nil {
		hotCancel()
		_ = conn.Close()
		t.Fatalf("model slug unexpectedly touched SQLite: %v", err)
	}
	record.InstanceID = "instance-a"
	if err := s.UpdateCorrelatedRequest(hotCtx, "req-missing-active", record); err != nil {
		hotCancel()
		_ = conn.Close()
		t.Fatalf("update unexpectedly touched SQLite: %v", err)
	}
	hotCancel()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	entry, exists := state.entries["req-missing-active"]
	gotInstance, gotSlug := "", ""
	if exists {
		gotInstance = entry.record.InstanceID
		gotSlug = entry.modelSlug
	}
	state.mu.Unlock()
	if !exists || gotInstance != "instance-a" || gotSlug != "public-model" {
		t.Fatalf("recovered entry exists=%v instance=%q slug=%q", exists, gotInstance, gotSlug)
	}

	record.StatusCode = 200
	if err := s.FinalizeCorrelatedRequest(ctx, "req-missing-active", nil, record); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachRequestLogContext(ctx, "req-missing-active", "session-a", "public-model"); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	state.mu.Lock()
	_, stillActive := state.activeEntries["req-missing-active"]
	state.mu.Unlock()
	if stillActive {
		t.Fatal("persisted request remained marked active")
	}

	var instanceID, modelSlug string
	if err := s.db.QueryRowContext(ctx, `SELECT r.instance_id,r.model_slug
		FROM inference_requests r
		JOIN inference_request_correlations c ON c.inference_request_id=r.id
		WHERE c.request_id=?`, "req-missing-active").Scan(&instanceID, &modelSlug); err != nil {
		t.Fatal(err)
	}
	if instanceID != "instance-a" || modelSlug != "public-model" {
		t.Fatalf("persisted instance=%q slug=%q", instanceID, modelSlug)
	}
}
