package observability

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
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

func TestPlaygroundSchemaExistsFromMigrations(t *testing.T) {
	service := playgroundTestService(t)
	ctx := context.Background()
	if err := service.ensurePlaygroundSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if !hasPlaygroundCorrelationColumn(ctx, service.db) {
		t.Fatal("expected playground_lifecycle_events.correlation_id from migrations")
	}
	if _, err := service.db.ExecContext(ctx, `INSERT INTO playground_lifecycle_events(event,instance_id,correlation_id) VALUES(?,?,?)`, LifecycleEviction, "victim", "trace"); err != nil {
		t.Fatalf("insert playground event: %v", err)
	}
}

func hasPlaygroundCorrelationColumn(ctx context.Context, db *sql.DB) bool {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(playground_lifecycle_events)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false
		}
		if name == "correlation_id" {
			return true
		}
	}
	return false
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
