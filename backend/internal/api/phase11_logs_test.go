package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePhase11Logs struct {
	lines []string
	events chan string
}

func (f *fakePhase11Logs) Logs(string) []string { return append([]string(nil), f.lines...) }
func (f *fakePhase11Logs) SubscribeLogs(string) ([]string, <-chan string, func()) {
	if f.events == nil { f.events = make(chan string) }
	return append([]string(nil), f.lines...), f.events, func() {}
}

func TestPhase11LogSearchAndValidation(t *testing.T) {
	source := &fakePhase11Logs{lines: []string{"[stdout] server ready", "[stderr] CUDA warning", "[manager] runtime READY", "legacy manager line"}}
	h := NewPhase11LogHandler(source)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/logs?instance_id=one&source=stderr&q=cuda&limit=10", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"source":"stderr"`) || !strings.Contains(w.Body.String(), "CUDA warning") || strings.Contains(w.Body.String(), "server ready") {
		t.Fatalf("search=%d %s", w.Code, w.Body.String())
	}

	for _, path := range []string{
		"/api/v1/logs",
		"/api/v1/logs?instance_id=one&source=nope",
		"/api/v1/logs?instance_id=one&limit=0",
		"/api/v1/logs?instance_id=one&limit=2001",
		"/api/v1/logs?instance_id=one&limit=abc",
	} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusBadRequest { t.Fatalf("%s => %d %s", path, w.Code, w.Body.String()) }
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/logs?instance_id=one", nil))
	if w.Code != http.StatusMethodNotAllowed { t.Fatalf("method=%d", w.Code) }
}

func TestPhase11LogStreamAndHelpers(t *testing.T) {
	source := &fakePhase11Logs{lines: []string{"[stdout] old", "[manager] loading", "[stderr] warning"}}
	h := NewPhase11LogHandler(source)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream?instance_id=one&source=manager&limit=2", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(w.Body.String(), "event: log") || !strings.Contains(w.Body.String(), `"source":"manager"`) || strings.Contains(w.Body.String(), `"source":"stdout"`) {
		t.Fatalf("stream=%d %s", w.Code, w.Body.String())
	}

	if entry := parseLogEntry("[stdout] hello"); entry.Source != "stdout" || entry.Text != "hello" { t.Fatalf("entry=%+v", entry) }
	if entry := parseLogEntry("plain"); entry.Source != "manager" || entry.Text != "plain" { t.Fatalf("legacy=%+v", entry) }
	if source, ok := normalizeLogSource(""); !ok || source != "all" { t.Fatalf("default source=%q %v", source, ok) }
	if _, ok := normalizeLogSource("invalid"); ok { t.Fatal("invalid source accepted") }
	if !logEntryMatches(logEntry{Source:"stderr", Text:"CUDA Error"}, "stderr", "cuda") { t.Fatal("expected case-insensitive match") }
	if logEntryMatches(logEntry{Source:"stdout", Text:"ok"}, "stderr", "") { t.Fatal("unexpected source match") }
	filtered := filterLogEntries([]string{"[stdout] 1", "[stdout] 2", "[stdout] 3"}, "stdout", "", 2)
	if len(filtered) != 2 || filtered[0].Text != "2" || filtered[1].Text != "3" { t.Fatalf("tail=%+v", filtered) }
	if got := filterLogEntries([]string{"x"}, "all", "", 0); len(got) != 0 { t.Fatalf("zero limit=%v", got) }
}
