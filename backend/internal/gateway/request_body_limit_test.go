package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
