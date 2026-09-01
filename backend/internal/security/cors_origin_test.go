package security

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginAllowedAcceptsSameHostManagerFrontendPort(t *testing.T) {
	network := NewNetwork(testSecuritySettings(t))

	r := httptest.NewRequest(http.MethodGet, "http://192.168.60.5:8888/api/v1/auth/bootstrap", nil)
	r.Host = "192.168.60.5:8888"

	if !network.OriginAllowed(r, "http://192.168.60.5:3000") {
		t.Fatal("same-host manager frontend origin should be allowed")
	}
	if network.OriginAllowed(r, "http://192.168.60.5:5173") {
		t.Fatal("arbitrary same-host ports must remain blocked")
	}
	if network.OriginAllowed(r, "http://192.168.60.6:3000") {
		t.Fatal("foreign host on manager frontend port must remain blocked")
	}
	if network.OriginAllowed(r, "https://192.168.60.5:3000") {
		t.Fatal("scheme mismatch must remain blocked")
	}
}

func TestRequestHostnameHandlesIPv6AndPorts(t *testing.T) {
	cases := map[string]string{
		"192.168.60.5:8888":  "192.168.60.5",
		"manager.local":      "manager.local",
		"[2001:db8::1]:8888": "2001:db8::1",
		"[2001:db8::1]":      "2001:db8::1",
	}
	for input, want := range cases {
		if got := requestHostname(input); got != want {
			t.Fatalf("requestHostname(%q)=%q want=%q", input, got, want)
		}
	}
}
