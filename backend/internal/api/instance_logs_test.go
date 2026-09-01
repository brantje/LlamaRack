package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testLogTimestamp = "2026-08-28T12:34:56.123456789Z"

func testLogLine(source, text string) string {
	return "[" + source + "]\t" + testLogTimestamp + "\t" + text
}

type fakeInstanceLogs struct {
	lines  []string
	events chan string
}

func (f *fakeInstanceLogs) Logs(string) []string { return append([]string(nil), f.lines...) }
func (f *fakeInstanceLogs) SubscribeLogs(string) ([]string, <-chan string, func()) {
	if f.events == nil {
		f.events = make(chan string)
	}
	return append([]string(nil), f.lines...), f.events, func() {}
}

func TestInstanceLogSearchAndValidation(t *testing.T) {
	source := &fakeInstanceLogs{lines: []string{
		testLogLine("stdout", "server ready"),
		testLogLine("stderr", "CUDA warning"),
		testLogLine("manager", "runtime READY"),
		"invalid untimestamped line",
	}}
	h := NewInstanceLogHandler(source)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/logs?instance_id=one&source=stderr&q=cuda&limit=10", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"source":"stderr"`) || !strings.Contains(w.Body.String(), `"timestamp":"`+testLogTimestamp+`"`) || !strings.Contains(w.Body.String(), "CUDA warning") || strings.Contains(w.Body.String(), "server ready") {
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
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s => %d %s", path, w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/logs?instance_id=one", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", w.Code)
	}
}

func TestInstanceLogStreamAndHelpers(t *testing.T) {
	source := &fakeInstanceLogs{lines: []string{
		testLogLine("stdout", "old"),
		testLogLine("manager", "loading"),
		testLogLine("stderr", "warning"),
	}}
	h := NewInstanceLogHandler(source)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream?instance_id=one&source=manager&limit=2", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(w.Body.String(), "event: log") || !strings.Contains(w.Body.String(), `"source":"manager"`) || !strings.Contains(w.Body.String(), `"timestamp":"`+testLogTimestamp+`"`) || strings.Contains(w.Body.String(), `"source":"stdout"`) {
		t.Fatalf("stream=%d %s", w.Code, w.Body.String())
	}

	entry, ok := parseLogEntry(testLogLine("stdout", "hello"))
	if !ok || entry.Source != "stdout" || entry.Timestamp != testLogTimestamp || entry.Text != "hello" {
		t.Fatalf("entry=%+v ok=%v", entry, ok)
	}
	for _, malformed := range []string{"[stdout] hello", "plain", "[bogus]\t" + testLogTimestamp + "\thello", "[stdout]\tbad-time\thello"} {
		if _, ok := parseLogEntry(malformed); ok {
			t.Fatalf("malformed log accepted: %q", malformed)
		}
	}
	if source, ok := normalizeLogSource(""); !ok || source != "all" {
		t.Fatalf("default source=%q %v", source, ok)
	}
	if _, ok := normalizeLogSource("invalid"); ok {
		t.Fatal("invalid source accepted")
	}
	if !logEntryMatches(logEntry{Source: "stderr", Timestamp: testLogTimestamp, Text: "CUDA Error"}, "stderr", "cuda") {
		t.Fatal("expected case-insensitive match")
	}
	if logEntryMatches(logEntry{Source: "stdout", Timestamp: testLogTimestamp, Text: "ok"}, "stderr", "") {
		t.Fatal("unexpected source match")
	}
	filtered := filterLogEntries([]string{testLogLine("stdout", "1"), testLogLine("stdout", "2"), testLogLine("stdout", "3")}, "stdout", "", 2)
	if len(filtered) != 2 || filtered[0].Text != "2" || filtered[1].Text != "3" {
		t.Fatalf("tail=%+v", filtered)
	}
	if got := filterLogEntries([]string{"x"}, "all", "", 10); len(got) != 0 {
		t.Fatalf("malformed=%v", got)
	}
	if got := filterLogEntries([]string{testLogLine("stdout", "x")}, "all", "", 0); len(got) != 0 {
		t.Fatalf("zero limit=%v", got)
	}
}
