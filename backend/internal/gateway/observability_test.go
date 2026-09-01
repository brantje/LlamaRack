package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/observability"
)

func TestUsageParsingJSONSSETimings(t *testing.T) {
	usage := parseUsage([]byte(`{"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`))
	if usage.prompt != 4 || usage.generated != 6 || usage.total != 10 {
		t.Fatalf("usage=%+v", usage)
	}
	usage = parseUsage([]byte("data: {\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"total_tokens\":5}}\n\ndata: [DONE]\n"))
	if usage.prompt != 2 || usage.generated != 3 || usage.total != 5 {
		t.Fatalf("sse=%+v", usage)
	}
	usage = parseUsage([]byte(`{"timings":{"prompt_n":7,"prompt_per_second":11.5,"predicted_n":8,"predicted_per_second":9.5}}`))
	if usage.prompt != 7 || usage.generated != 8 || usage.total != 15 || usage.generationTPS == nil || *usage.generationTPS != 9.5 || usage.promptTPS == nil || *usage.promptTPS != 11.5 {
		t.Fatalf("timings=%+v", usage)
	}
	usage = parseUsage([]byte("not json"))
	if usage.total != 0 {
		t.Fatalf("invalid=%+v", usage)
	}
}

func TestResponseObserverAndErrors(t *testing.T) {
	base := httptest.NewRecorder()
	observed := newResponseObserver(base, false)
	observed.WriteHeader(http.StatusTeapot)
	_, _ = observed.Write([]byte(`{"error":{"message":" bad\u0001 error  message "}}`))
	observed.Flush()
	if observed.StatusCode() != http.StatusTeapot || observed.FirstByte().IsZero() || !strings.Contains(string(observed.Bytes()), "error") || observed.Unwrap() != base {
		t.Fatalf("observer status=%d body=%s", observed.StatusCode(), observed.Bytes())
	}
	if got := responseError(http.StatusTeapot, observed.Bytes()); got != "bad error message" {
		t.Fatalf("error=%q", got)
	}
	if got := responseError(500, []byte("no json")); got != "HTTP 500" {
		t.Fatalf("fallback=%q", got)
	}
	if got := sanitizeError(strings.Repeat("x", 600)); len(got) != 512 {
		t.Fatalf("sanitize length=%d", len(got))
	}
}

func TestMetadataCaptureIsBoundedAndFullCaptureIsNot(t *testing.T) {
	chunk := make([]byte, metadataResponseCaptureLimit+128)
	base := httptest.NewRecorder()
	observed := newResponseObserver(base, false)
	_, _ = observed.Write(chunk)
	if len(observed.Bytes()) != metadataResponseCaptureLimit {
		t.Fatalf("bounded=%d", len(observed.Bytes()))
	}
	base = httptest.NewRecorder()
	observed = newResponseObserver(base, true)
	_, _ = observed.Write(chunk)
	if len(observed.Bytes()) != len(chunk) {
		t.Fatalf("full=%d", len(observed.Bytes()))
	}
}

func TestPersistHelper(t *testing.T) {
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := observability.New(db)
	g := &Gateway{observability: service}
	now := time.Now().UnixMilli()
	g.persist(context.Background(), "lcm_persist", nil, observability.RequestRecord{StartedAt: now, FinishedAt: now, InstanceID: "one", Endpoint: "/v1/completions", StatusCode: 200, DurationMS: 1})
	items, err := service.ListRequests(context.Background(), observability.RequestFilters{InstanceID: "one"})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	correlated, err := service.GetRequestByRequestID(context.Background(), "lcm_persist")
	if err != nil || correlated.ID != items[0].ID {
		t.Fatalf("correlated=%+v err=%v", correlated, err)
	}
	(&Gateway{}).persist(context.Background(), "lcm_unused", nil, observability.RequestRecord{})
}

func TestNumberAndUsageHelpers(t *testing.T) {
	if value := intValue(map[string]any{"x": 12.0}, "missing", "x"); value != 12 {
		t.Fatal(value)
	}
	if _, ok := numberValue("12"); ok {
		t.Fatal("string should not be numeric")
	}
	value := usageFromObject(map[string]any{"usage": map[string]any{"prompt_tokens": 1.0, "completion_tokens": 2.0}})
	if value.total != 3 {
		t.Fatalf("total=%d", value.total)
	}
	value = usageFromObject(map[string]any{"timings": map[string]any{"prompt_n": 2.0, "prompt_ms": 4.0, "predicted_n": 3.0, "predicted_ms": 6.0}})
	if value.promptTPS == nil || value.generationTPS == nil || *value.promptTPS != 500 || *value.generationTPS != 500 {
		t.Fatalf("derived timings=%+v", value)
	}
}
