package gateway

import (
	"net/http"
	"strconv"
	"testing"
)

func requireOverheadHeader(t *testing.T, header http.Header) float64 {
	t.Helper()
	raw := header.Get(headerOverheadMS)
	if raw == "" {
		t.Fatalf("missing %s header", headerOverheadMS)
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("invalid %s=%q: %v", headerOverheadMS, raw, err)
	}
	if value < 0 {
		t.Fatalf("negative %s=%f", headerOverheadMS, value)
	}
	return value
}

func TestOverheadHeaderOnProxiedSuccessAndManagerError(t *testing.T) {
	fixture := newGatewayFixture(t, true)
	success := gatewayRequest(t, fixture.gateway, http.MethodPost, "/v1/chat/completions", fixture.secret, `{"model":"gateway-model"}`)
	if success.Code != http.StatusOK {
		t.Fatalf("success status=%d body=%s", success.Code, success.Body.String())
	}
	requireOverheadHeader(t, success.Header())

	denied := gatewayRequest(t, fixture.gateway, http.MethodPost, "/v1/chat/completions", "invalid", `{"model":"gateway-model"}`)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("denied status=%d body=%s", denied.Code, denied.Body.String())
	}
	requireOverheadHeader(t, denied.Header())
}

func TestOverheadHeaderIsAvailableForStreamingAtHeaderCommit(t *testing.T) {
	fixture := newGatewayFixture(t, true)
	response := gatewayRequest(t, fixture.gateway, http.MethodPost, "/v1/chat/completions", fixture.secret, `{"model":"gateway-model","stream":true}`)
	if response.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%s", response.Code, response.Body.String())
	}
	requireOverheadHeader(t, response.Header())
}
