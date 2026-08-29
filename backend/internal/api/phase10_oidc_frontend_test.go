package api

import (
	"net/url"
	"testing"
)

func TestOIDCFrontendExchangeURL(t *testing.T) {
	exchange := "https://manager.example.test/?oidc_exchange=one-time-code"
	if got, err := oidcFrontendExchangeURL(exchange, ""); err != nil || got != exchange {
		t.Fatalf("empty frontend URL should preserve exchange URL: got=%q err=%v", got, err)
	}

	got, err := oidcFrontendExchangeURL(exchange, "http://192.168.60.5:3000/app?source=oidc#ignored")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "http" || parsed.Host != "192.168.60.5:3000" || parsed.Path != "/app" {
		t.Fatalf("unexpected frontend redirect: %s", got)
	}
	if parsed.Query().Get("oidc_exchange") != "one-time-code" || parsed.Query().Get("source") != "oidc" || parsed.Fragment != "" {
		t.Fatalf("unexpected frontend redirect query/fragment: %s", got)
	}

	for _, invalid := range []string{"frontend.example.test", "javascript:alert(1)", "://bad"} {
		if err := validateOIDCFrontendURL(invalid); err == nil {
			t.Fatalf("expected invalid frontend URL %q", invalid)
		}
	}
	if _, err := oidcFrontendExchangeURL(exchange, "javascript:alert(1)"); err == nil {
		t.Fatal("invalid frontend URL should fail redirect construction")
	}
	if _, err := oidcFrontendExchangeURL("://bad", "https://frontend.example.test"); err == nil {
		t.Fatal("invalid exchange URL should fail")
	}
	if _, err := oidcFrontendExchangeURL("https://manager.example.test/", "https://frontend.example.test"); err == nil {
		t.Fatal("missing exchange code should fail")
	}
}
