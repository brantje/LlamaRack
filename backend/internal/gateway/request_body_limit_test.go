package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type countingReadCloser struct {
	reader io.Reader
	read   int
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

func (r *countingReadCloser) Close() error { return nil }

func TestOversizedRequestBodyReturnsAndPersists413(t *testing.T) {
	f := newGatewayFixture(t, false)
	body := strings.Repeat("x", maxRequestBodyBytes+1)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+f.secret)
	w := httptest.NewRecorder()
	f.gateway.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "body_too_large") {
		t.Fatalf("body=%s", w.Body.String())
	}
	requestID := w.Header().Get(headerRequestID)
	if requestID == "" {
		t.Fatal("missing manager request ID")
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if record.StatusCode != http.StatusRequestEntityTooLarge || record.Result != "error" || !strings.Contains(record.Error, "too large") {
		t.Fatalf("record=%+v", record)
	}
	if record.RequestBody != nil {
		t.Fatal("oversized request body must not be retained")
	}
}

func TestUnauthenticatedLargeRequestBodyUsesMetadataBudget(t *testing.T) {
	f := newGatewayFixture(t, false)
	body := &countingReadCloser{reader: strings.NewReader(strings.Repeat("x", preAuthRequestBodyBytes*4))}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Body = body
	r.ContentLength = -1
	r.Header.Set("Authorization", "Bearer invalid")
	w := httptest.NewRecorder()

	f.gateway.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if body.read > preAuthRequestBodyBytes {
		t.Fatalf("unauthenticated request read %d bytes; want <= %d", body.read, preAuthRequestBodyBytes)
	}
	requestID := w.Header().Get(headerRequestID)
	if requestID == "" {
		t.Fatal("missing manager request ID")
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if record.StatusCode != http.StatusUnauthorized || record.Result != "error" {
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

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get(headerTraceID); got != testTraceBody {
		t.Fatalf("trace header=%q want=%q", got, testTraceBody)
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
	if err != nil {
		t.Fatal(err)
	}
	if record.TraceID != testTraceBody || record.InstanceID != "missing-model" {
		t.Fatalf("record=%+v", record)
	}
}
