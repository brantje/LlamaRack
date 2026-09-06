package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestEffectiveRemoteAddressForwardingHeaderTrustAndFallbacks(t *testing.T) {
	ctx := context.Background()
	store := testSecuritySettings(t)
	if _, err := store.Set(ctx, settings.TrustedProxies, "10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	network := NewNetwork(store)

	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		xff        string
		xRealIP    string
		want       string
	}{
		{
			name:       "untrusted peer ignores all forwarding headers",
			remoteAddr: "203.0.113.10:4444",
			forwarded:  `for=198.51.100.1`,
			xff:        "198.51.100.2",
			xRealIP:    "198.51.100.3",
			want:       "203.0.113.10",
		},
		{
			name:       "forwarded takes precedence",
			remoteAddr: "10.0.0.2:1234",
			forwarded:  `for="[2001:0db8::1]:444";proto=https, for=10.0.0.3`,
			xff:        "198.51.100.2",
			xRealIP:    "198.51.100.3",
			want:       "2001:db8::1",
		},
		{
			name:       "x forwarded for takes precedence over real ip and strips port",
			remoteAddr: "10.0.0.2:1234",
			xff:        "203.0.113.9:555, 10.0.0.3",
			xRealIP:    "198.51.100.3",
			want:       "203.0.113.9",
		},
		{
			name:       "x real ip trusted fallback strips ipv4 port",
			remoteAddr: "10.0.0.2:1234",
			xRealIP:    "198.51.100.10:777",
			want:       "198.51.100.10",
		},
		{
			name:       "x real ip trusted fallback canonicalizes ipv6",
			remoteAddr: "10.0.0.2:1234",
			xRealIP:    "[2001:0db8::5]:8443",
			want:       "2001:db8::5",
		},
		{
			name:       "invalid forwarding metadata falls back to trusted peer",
			remoteAddr: "10.0.0.2:1234",
			forwarded:  "for=unknown",
			xff:        "not-an-ip",
			xRealIP:    "also-not-an-ip",
			want:       "10.0.0.2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://manager.local/api", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.forwarded != "" {
				r.Header.Set("Forwarded", tc.forwarded)
			}
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xRealIP != "" {
				r.Header.Set("X-Real-IP", tc.xRealIP)
			}
			if got := network.EffectiveRemoteAddress(r); got != tc.want {
				t.Fatalf("remote=%q want=%q", got, tc.want)
			}
		})
	}
}
