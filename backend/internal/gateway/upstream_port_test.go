package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpstreamPortHeaderUsesResolvedRuntime(t *testing.T) {
	fixture := newGatewayFixture(t, true)
	handler := WithUpstreamPortHeader(fixture.gateway.lifecycle, fixture.gateway)
	response := gatewayRequest(t, handler, http.MethodPost, "/v1/chat/completions", fixture.secret, `{"model":"gateway-model"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get(headerInstance) != "gateway-model" {
		t.Fatalf("instance headers=%v", response.Header())
	}
	if response.Header().Get(headerUpstreamPort) == "" {
		t.Fatalf("upstream port missing: %v", response.Header())
	}
}

func TestUpstreamPortHeaderPassThroughBranches(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	if got := WithUpstreamPortHeader(nil, next); got == nil {
		t.Fatal("nil lifecycle should return the original handler")
	}

	recorder := httptest.NewRecorder()
	writer := &upstreamPortResponseWriter{ResponseWriter: recorder}
	writer.WriteHeader(http.StatusAccepted)
	if recorder.Code != http.StatusAccepted || recorder.Header().Get(headerUpstreamPort) != "" {
		t.Fatalf("unexpected response: code=%d headers=%v", recorder.Code, recorder.Header())
	}

	recorder = httptest.NewRecorder()
	writer = &upstreamPortResponseWriter{ResponseWriter: recorder}
	_, _ = writer.Write([]byte("ok"))
	writer.Flush()
	if recorder.Body.String() != "ok" || writer.Unwrap() != recorder {
		t.Fatalf("writer passthrough body=%q", recorder.Body.String())
	}
}
