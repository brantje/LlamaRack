package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/systemlog"
)

type branchSystemLogStore struct {
	snapshot []systemlog.Entry
	events   []systemlog.Entry
}

func (s *branchSystemLogStore) Snapshot(int) []systemlog.Entry {
	return append([]systemlog.Entry(nil), s.snapshot...)
}

func (s *branchSystemLogStore) Subscribe(int) ([]systemlog.Entry, <-chan systemlog.Entry, func()) {
	ch := make(chan systemlog.Entry, len(s.events))
	for _, entry := range s.events {
		ch <- entry
	}
	close(ch)
	return append([]systemlog.Entry(nil), s.snapshot...), ch, func() {}
}

type noFlushWriter struct {
	header http.Header
	status int
	body   strings.Builder
}

func (w *noFlushWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *noFlushWriter) WriteHeader(status int)         { w.status = status }
func (w *noFlushWriter) Write(body []byte) (int, error) { return w.body.Write(body) }

type failWriteFlusher struct {
	header http.Header
	status int
}

func (w *failWriteFlusher) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *failWriteFlusher) WriteHeader(status int)    { w.status = status }
func (w *failWriteFlusher) Write([]byte) (int, error) { return 0, context.Canceled }
func (w *failWriteFlusher) Flush()                    {}

func TestSystemLogStreamConsumesLiveEventsAndSkipsNonMatches(t *testing.T) {
	store := &branchSystemLogStore{
		snapshot: []systemlog.Entry{{Timestamp: testLogTimestamp, Level: systemlog.Info, Source: "manager", Message: "snapshot"}},
		events: []systemlog.Entry{
			{Timestamp: testLogTimestamp, Level: systemlog.Debug, Source: "telemetry", Message: "ignored"},
			{Timestamp: testLogTimestamp, Level: systemlog.Warn, Source: "manager", Message: "live warning"},
		},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream?scope=system&source=manager&level=WARN", nil).WithContext(context.Background())
	NewSystemLogHandler(store).ServeHTTP(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK || strings.Contains(body, `"message":"snapshot"`) || strings.Contains(body, "ignored") || !strings.Contains(body, "event: snapshot") || !strings.Contains(body, "live warning") {
		t.Fatalf("stream=%d %s", w.Code, body)
	}
}

func TestSystemLogStreamRejectsWriterWithoutFlushing(t *testing.T) {
	w := &noFlushWriter{}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream?scope=system", nil)
	NewSystemLogHandler(&branchSystemLogStore{}).ServeHTTP(w, r)
	if w.status != http.StatusInternalServerError || !strings.Contains(w.body.String(), "streaming unsupported") {
		t.Fatalf("status=%d body=%q", w.status, w.body.String())
	}
}

func TestSystemLogStreamStopsWhenSnapshotWriteFails(t *testing.T) {
	w := &failWriteFlusher{}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream?scope=system", nil)
	NewSystemLogHandler(&branchSystemLogStore{
		snapshot: []systemlog.Entry{{Timestamp: testLogTimestamp, Level: systemlog.Info, Source: "manager", Message: "history"}},
	}).ServeHTTP(w, r)
	if w.status != http.StatusOK {
		t.Fatalf("status=%d", w.status)
	}
}
