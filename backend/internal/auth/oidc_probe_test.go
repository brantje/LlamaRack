package auth

import (
	"strings"
	"testing"
)

func TestOIDCTestProviderInput(t *testing.T) {
	f := newOIDCFixture(t)
	idp := newTestOIDCProvider(t)
	f.manager.client = idp.server.Client()

	secret := "client-secret"
	if err := f.manager.TestProviderInput(t.Context(), idp.input(&secret)); err != nil {
		t.Fatalf("valid provider draft: %v", err)
	}

	if err := f.manager.TestProviderInput(t.Context(), OIDCProviderInput{}); err == nil {
		t.Fatal("invalid provider draft should fail validation")
	}

	blankSecret := " "
	if err := f.manager.TestProviderInput(t.Context(), idp.input(&blankSecret)); err == nil || !strings.Contains(err.Error(), "client_secret is required") {
		t.Fatalf("blank client secret error=%v", err)
	}

	badDiscovery := idp.input(&secret)
	badDiscovery.DiscoveryURL = idp.server.URL + "/missing"
	if err := f.manager.TestProviderInput(t.Context(), badDiscovery); err == nil {
		t.Fatal("invalid discovery endpoint should fail")
	}

	badJWKS := idp.input(&secret)
	badJWKS.AuthorizationEndpoint = idp.server.URL + "/authorize"
	badJWKS.TokenEndpoint = idp.server.URL + "/token"
	badJWKS.JWKSURL = idp.server.URL + "/missing"
	if err := f.manager.TestProviderInput(t.Context(), badJWKS); err == nil {
		t.Fatal("invalid JWKS endpoint should fail")
	}
}
