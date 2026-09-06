package gateway

import (
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

type overheadContextKey struct{}

type overheadTracker struct {
	started          time.Time
	upstreamNanos    atomic.Int64
}

func newOverheadTracker(started time.Time) *overheadTracker {
	return &overheadTracker{started: started}
}

func (t *overheadTracker) addUpstream(duration time.Duration) {
	if t == nil || duration <= 0 {
		return
	}
	t.upstreamNanos.Add(int64(duration))
}

func (t *overheadTracker) overhead(now time.Time) time.Duration {
	if t == nil || t.started.IsZero() {
		return 0
	}
	value := now.Sub(t.started) - time.Duration(t.upstreamNanos.Load())
	if value < 0 {
		return 0
	}
	return value
}

type overheadResponseWriter struct {
	http.ResponseWriter
	tracker *overheadTracker
	wroteHeader bool
}

func (w *overheadResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *overheadResponseWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		setProductHeader(w.Header(), headerOverheadMS, metricFloat(milliseconds(w.tracker.overhead(time.Now()))))
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *overheadResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *overheadResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

type overheadTransport struct {
	base    http.RoundTripper
	tracker *overheadTracker
}

func (t overheadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	started := time.Now()
	response, err := base.RoundTrip(request)
	t.tracker.addUpstream(time.Since(started))
	if response != nil && response.Body != nil {
		response.Body = &upstreamTimingBody{ReadCloser: response.Body, tracker: t.tracker}
	}
	return response, err
}

type upstreamTimingBody struct {
	io.ReadCloser
	tracker *overheadTracker
}

func (b *upstreamTimingBody) Read(p []byte) (int, error) {
	started := time.Now()
	n, err := b.ReadCloser.Read(p)
	b.tracker.addUpstream(time.Since(started))
	return n, err
}

func (b *upstreamTimingBody) Close() error {
	started := time.Now()
	err := b.ReadCloser.Close()
	b.tracker.addUpstream(time.Since(started))
	return err
}
