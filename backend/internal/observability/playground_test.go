package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
)

func playgroundTestService(t *testing.T) *Service {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db)
}

func TestPlaygroundDiagnosticsUsesRequestRecordAndCorrelatedLifecycle(t *testing.T) {
	service := playgroundTestService(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	ttft := 125.0
	rate := 42.5
	traceID := "11111111-1111-4111-8111-111111111111"
	record := RequestRecord{
		StartedAt: now - 1000, FinishedAt: now + 1000, InstanceID: "target", Endpoint: "/v1/chat/completions", TraceID: traceID,
		StatusCode: http.StatusOK, Result: "success", DurationMS: 900, TTFTMS: &ttft,
		PromptTokens: 12, GeneratedTokens: 24, TotalTokens: 36, TokensPerSecond: &rate,
		LoadDurationMS: 320, Autoloaded: true,
	}
	if err := service.FinalizeCorrelatedRequest(ctx, "req-playground", nil, record); err != nil {
		t.Fatal(err)
	}
	correlated := lifecycle.WithRequestCorrelation(ctx, traceID)
	if err := service.RecordLifecycle(correlated, LifecycleEviction, "victim-a", 0); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordLifecycle(correlated, LifecycleEviction, "victim-a", 0); err != nil {
		t.Fatal(err)
	}
	other := lifecycle.WithRequestCorrelation(ctx, "22222222-2222-4222-8222-222222222222")
	if err := service.RecordLifecycle(other, LifecycleEviction, "victim-from-concurrent-request", 0); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordLifecycle(correlated, LifecycleLoad, "target", time.Second); err != nil {
		t.Fatal(err)
	}

	diagnostics, err := service.PlaygroundDiagnostics(ctx, "req-playground")
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics.Request.InstanceID != "target" || diagnostics.Request.PromptTokens != 12 || diagnostics.Request.GeneratedTokens != 24 {
		t.Fatalf("request=%+v", diagnostics.Request)
	}
	if got := diagnostics.StateTrace; len(got) != 3 || got[0] != "UNLOADED" || got[1] != "STARTING" || got[2] != "READY" {
		t.Fatalf("state trace=%v", got)
	}
	if got := diagnostics.EvictionsTriggered; len(got) != 1 || got[0] != "victim-a" {
		t.Fatalf("evictions=%v", got)
	}
}

func TestPlaygroundStateTraceVariants(t *testing.T) {
	if got := requestStateTrace(RequestRecord{}); got != nil {
		t.Fatalf("empty=%v", got)
	}
	if got := requestStateTrace(RequestRecord{InstanceID: "ready"}); len(got) != 1 || got[0] != "READY" {
		t.Fatalf("ready=%v", got)
	}
	if got := requestStateTrace(RequestRecord{InstanceID: "failed", Autoloaded: true, Result: "error", StatusCode: 503}); len(got) != 3 || got[2] != "FAILED" {
		t.Fatalf("failed=%v", got)
	}
}

func TestPlaygroundLifecycleRecorderIgnoresUnrelatedEvents(t *testing.T) {
	service := playgroundTestService(t)
	ctx := context.Background()
	if err := service.recordPlaygroundLifecycleEvent(ctx, LifecycleLoad, "one"); err != nil {
		t.Fatal(err)
	}
	if err := service.recordPlaygroundLifecycleEvent(ctx, LifecycleEviction, "victim-without-correlation"); err != nil {
		t.Fatal(err)
	}
	if err := service.recordPlaygroundLifecycleEvent(lifecycle.WithRequestCorrelation(ctx, "33333333-3333-4333-8333-333333333333"), LifecycleEviction, " "); err != nil {
		t.Fatal(err)
	}
	if err := service.ensurePlaygroundSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM playground_lifecycle_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("count=%d", count)
	}
	if got := lifecycle.RequestCorrelationFromContext(nil); got != "" {
		t.Fatalf("nil correlation=%q", got)
	}
	if got := lifecycle.RequestCorrelationFromContext(lifecycle.WithRequestCorrelation(ctx, "  ")); got != "" {
		t.Fatalf("empty correlation=%q", got)
	}
}

func TestPlaygroundSchemaMigratesDraftTable(t *testing.T) {
	service := playgroundTestService(t)
	ctx := context.Background()
	if _, err := service.db.ExecContext(ctx, `CREATE TABLE playground_lifecycle_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		recorded_at INTEGER NOT NULL,
		event TEXT NOT NULL,
		instance_id TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if err := service.ensurePlaygroundSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.ExecContext(ctx, `INSERT INTO playground_lifecycle_events(recorded_at,event,instance_id,correlation_id) VALUES(?,?,?,?)`, time.Now().UnixMilli(), LifecycleEviction, "victim", "trace"); err != nil {
		t.Fatalf("migration did not add correlation_id: %v", err)
	}
}

func TestPlaygroundEvictionsWithoutTraceAreEmpty(t *testing.T) {
	service := playgroundTestService(t)
	got, err := service.playgroundEvictions(context.Background(), RequestRecord{InstanceID: "target"})
	if err != nil || got != nil {
		t.Fatalf("evictions=%v err=%v", got, err)
	}
}

func TestPlaygroundDiagnosticsHandler(t *testing.T) {
	service := playgroundTestService(t)
	handler := NewPlaygroundDiagnosticsHandler(service)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/observability/playground/x", nil)
	req.SetPathValue("request_id", "x")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status=%d", res.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/observability/playground/", nil)
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("empty status=%d body=%s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/observability/playground/missing", nil)
	req.SetPathValue("request_id", "missing")
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", res.Code, res.Body.String())
	}
}
