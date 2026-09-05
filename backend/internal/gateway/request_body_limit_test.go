package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestUnauthenticatedLargeRequestDoesNotReadPastPreAuthLimit(t *testing.T) {
	f := newGatewayFixture(t, true)
	body := newTrackingBody(`{"model":"missing-model","padding":"` + strings.Repeat("x", preAuthRequestBodyBytes*2) + `"}`)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
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
	if record.TraceID != testTraceBody || record.InstanceID != "missing-model" {
		t.Fatalf("record=%+v", record)
	}
}
