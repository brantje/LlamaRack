package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDatabaseFailuresAreReportedWithoutPanics(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	if err := s.db.Close(); err != nil { t.Fatal(err) }

	now := time.Now().UnixMilli()
	if err := s.RecordRequest(ctx, RequestRecord{StartedAt: now, FinishedAt: now, InstanceID: "one", Endpoint: "/v1/completions", StatusCode: 200}); err == nil {
		t.Fatal("record should fail when database is closed")
	}
	if _, err := s.Counters(ctx); err == nil { t.Fatal("counters should fail when database is closed") }
	if _, err := s.Summary(ctx, now-1000); err == nil { t.Fatal("summary should fail when database is closed") }
	if _, err := s.ListRequests(ctx, RequestFilters{}); err == nil { t.Fatal("list should fail when database is closed") }
	if _, err := s.Timeseries(ctx, "requests", now-1000, 60); err == nil { t.Fatal("timeseries should fail when database is closed") }
	if err := s.Prune(ctx, 30); err == nil { t.Fatal("prune should fail when database is closed") }

	h := NewManagementHandler(s)
	for _, path := range []string{
		"/api/v1/observability/summary?since=1",
		"/api/v1/observability/requests",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusInternalServerError { t.Fatalf("%s => %d %s", path, w.Code, w.Body.String()) }
	}

	metrics := NewMetricsHandler(s, nil)
	w := httptest.NewRecorder()
	metrics.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusInternalServerError { t.Fatalf("metrics => %d %s", w.Code, w.Body.String()) }
}

func TestSingleValuePercentiles(t *testing.T) {
	values := percentiles([]float64{42})
	if values.P50 == nil || values.P95 == nil || values.P99 == nil || *values.P50 != 42 || *values.P95 != 42 || *values.P99 != 42 {
		t.Fatalf("percentiles=%+v", values)
	}
}
