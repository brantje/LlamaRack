package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type trackingBody struct {
	reader *strings.Reader
	read   int
}

func newTrackingBody(value string) *trackingBody {
	return &trackingBody{reader: strings.NewReader(value)}
}

func (b *trackingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *trackingBody) Close() error { return nil }

type stallingBody struct {
	reads     atomic.Int32
	startOnce sync.Once
	started   chan struct{}
	unblock   chan struct{}
}

func newStallingBody() *stallingBody {
	return &stallingBody{started: make(chan struct{}), unblock: make(chan struct{})}
}

func (b *stallingBody) Read([]byte) (int, error) {
	b.reads.Add(1)
	b.startOnce.Do(func() { close(b.started) })
	<-b.unblock
	return 0, io.EOF
}

func (b *stallingBody) Close() error { return nil }

func TestUnauthenticatedRequestDoesNotReadBody(t *testing.T) {
	f := newGatewayFixture(t, true)
	body := newTrackingBody(`{"model":"missing-model","stream":true,"padding":"` + strings.Repeat("x", preAuthRequestBodyBytes*2) + `","litellm_metadata":{"trace_id":"` + testTraceBody + `"}}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	r.Header.Set("Authorization", "Bearer invalid")
	r.Header.Set("User-Agent", "auth-before-body/1.0")
	r.Header.Set(headerTraceID, testTraceHeader)
	w := httptest.NewRecorder()

	f.gateway.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if body.read != 0 {
		t.Fatalf("unauthenticated request read %d bytes; want 0", body.read)
	}
	if got := w.Header().Get(headerTraceID); got != testTraceHeader {
		t.Fatalf("trace header=%q want=%q", got, testTraceHeader)
	}
	requestID := w.Header().Get(headerRequestID)
	if requestID == "" {
		t.Fatal("missing manager request ID")
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if record.StatusCode != http.StatusUnauthorized || record.Result != "error" || record.InstanceID != "" || record.Streaming || record.APIKey != nil {
		t.Fatalf("record=%+v", record)
	}
	if record.CallType != "chat_completion" || record.TraceID != testTraceHeader || record.UserAgent != "auth-before-body/1.0" {
		t.Fatalf("header metadata=%+v", record)
	}
	identity, err := f.observability.RequestModelIdentity(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ModelSlug != "" || identity.InstanceID != "" {
		t.Fatalf("body-derived identity=%+v", identity)
	}
}

func TestUnauthenticatedStalledBodyIsRejectedWithoutWaiting(t *testing.T) {
	f := newGatewayFixture(t, true)
	body := newStallingBody()
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	r.Header.Set("Authorization", "Bearer invalid")
	r.Header.Set(headerTraceID, testTraceHeader)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		f.gateway.ServeHTTP(w, r)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("invalid credentials waited for a stalled request body")
	}

	if body.reads.Load() != 0 {
		t.Fatalf("unauthenticated request read body %d times; want 0", body.reads.Load())
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(headerTraceID); got != testTraceHeader {
		t.Fatalf("trace header=%q want=%q", got, testTraceHeader)
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
	if err != nil {
		t.Fatal(err)
	}
	if record.StatusCode != http.StatusUnauthorized || record.Result != "error" || record.InstanceID != "" || record.Streaming {
		t.Fatalf("record=%+v", record)
	}
}

func TestAuthenticatedLargeRequestRecoversBodyTraceAfterAuthentication(t *testing.T) {
	f := newGatewayFixture(t, false)
	body := `{"model":"missing-model","padding":"` + strings.Repeat("x", preAuthRequestBodyBytes+1024) + `","litellm_metadata":{"trace_id":"` + testTraceBody + `"}}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+f.secret)
	w := httptest.NewRecorder()

	f.gateway.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(headerTraceID); got != testTraceBody {
		t.Fatalf("trace header=%q want=%q", got, testTraceBody)
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
	if err != nil {
		t.Fatal(err)
	}
	if record.TraceID != testTraceBody || record.InstanceID != "" {
		t.Fatalf("record=%+v", record)
	}
}
