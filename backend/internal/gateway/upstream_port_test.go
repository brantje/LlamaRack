package gateway

import (
	"net/http"
	"strings"
	"testing"
)

func TestInferenceResponsesDoNotExposeUpstreamPort(t *testing.T) {
	fixture := newGatewayFixture(t, true)
	response := gatewayRequest(t, fixture.gateway, http.MethodPost, "/v1/chat/completions", fixture.secret, `{"model":"gateway-model"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get(headerInstance) != "gateway-model" {
		t.Fatalf("instance headers=%v", response.Header())
	}
	assertNoUpstreamPortHeader(t, response.Header())
}

func assertNoUpstreamPortHeader(t *testing.T, header http.Header) {
	t.Helper()
	for name := range header {
		if strings.Contains(strings.ToLower(name), "upstream-port") {
			t.Fatalf("worker port header leaked: %s=%q", name, header.Get(name))
		}
	}
}
