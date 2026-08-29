package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOIDCResolveProviderAcceptsCanonicalTrailingSlash(t *testing.T) {
	f := newOIDCFixture(t)
	var discoveryIssuer string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 discoveryIssuer,
			"authorization_endpoint": server.URL + "/authorize",
			"token_endpoint":         server.URL + "/token",
			"jwks_uri":               server.URL + "/jwks",
		})
	}))
	defer server.Close()
	f.manager.client = server.Client()

	discoveryIssuer = server.URL + "/"
	resolved, err := f.manager.resolveProvider(t.Context(), OIDCProvider{Issuer: server.URL})
	if err != nil {
		t.Fatalf("trailing-slash issuer should be accepted: %v", err)
	}
	if resolved.Issuer != discoveryIssuer {
		t.Fatalf("resolved issuer=%q want canonical discovery issuer %q", resolved.Issuer, discoveryIssuer)
	}

	discoveryIssuer = server.URL + "/different"
	if _, err := f.manager.resolveProvider(t.Context(), OIDCProvider{Issuer: server.URL}); err == nil || !strings.Contains(err.Error(), "issuer does not match") {
		t.Fatalf("different discovery issuer error=%v", err)
	}
}
