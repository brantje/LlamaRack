package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestOIDCCallbackHandlesConfiguredFrontendURL(t *testing.T) {
	f := newAPIOIDCFixture(t)
	if _, err := f.settings.Set(t.Context(), settings.ExternalURL, "https://manager.example.test"); err != nil {
		t.Fatal(err)
	}
	secret := "client-secret"
	provider, err := f.oidc.CreateProvider(t.Context(), auth.OIDCProviderInput{
		Name: "Frontend callback", Enabled: true, Issuer: f.idp.URL,
		ClientID: "manager", ClientSecret: &secret, Scopes: []string{"openid"},
	})
	if err != nil {
		t.Fatal(err)
	}

	start := adminRequest(t, f.raw, http.MethodGet, "/api/v1/auth/oidc/"+provider.ID+"/start", nil, nil, nil)
	if start.Code != http.StatusFound {
		t.Fatalf("OIDC start=%d body=%s", start.Code, start.Body.String())
	}
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := location.Query().Get("state")
	if state == "" {
		t.Fatal("OIDC start did not return state")
	}
	var stateCookie *http.Cookie
	for _, cookie := range start.Result().Cookies() {
		if cookie.Name == oidcStateCookie {
			stateCookie = cookie
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("OIDC start did not set state cookie")
	}

	// Settings validation normally prevents this value. Inject it directly to
	// verify a bad persisted/configured frontend destination fails closed before
	// the provider authorization code is consumed.
	if _, err := f.settings.Set(t.Context(), settings.FrontendURL, "javascript:alert(1)"); err != nil {
		t.Fatal(err)
	}
	callback := "/api/v1/auth/oidc/" + provider.ID + "/callback?state=" + url.QueryEscape(state) + "&code=provider-code"
	w := adminRequest(t, f.raw, http.MethodGet, callback, nil, []*http.Cookie{stateCookie}, nil)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "OIDC authentication failed") {
		t.Fatalf("invalid frontend callback=%d body=%s", w.Code, w.Body.String())
	}

	// A valid separate frontend destination passes frontend validation and lets
	// the normal backend callback continue. This fixture intentionally has no
	// token endpoint, so the provider exchange then fails with the normal 401.
	if _, err := f.settings.Set(t.Context(), settings.FrontendURL, "http://192.168.60.5:3000"); err != nil {
		t.Fatal(err)
	}
	w = adminRequest(t, f.raw, http.MethodGet, callback, nil, []*http.Cookie{stateCookie}, nil)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "OIDC authentication failed") {
		t.Fatalf("valid frontend callback provider failure=%d body=%s", w.Code, w.Body.String())
	}
}

func TestOIDCCallbackIgnoresPreviousStateCookieName(t *testing.T) {
	f := newAPIOIDCFixture(t)
	if _, err := f.settings.Set(t.Context(), settings.ExternalURL, "https://manager.example.test"); err != nil {
		t.Fatal(err)
	}
	secret := "client-secret"
	provider, err := f.oidc.CreateProvider(t.Context(), auth.OIDCProviderInput{
		Name: "Previous cookie", Enabled: true, Issuer: f.idp.URL,
		ClientID: "manager", ClientSecret: &secret, Scopes: []string{"openid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := adminRequest(t, f.raw, http.MethodGet, "/api/v1/auth/oidc/"+provider.ID+"/start", nil, nil, nil)
	if start.Code != http.StatusFound {
		t.Fatalf("OIDC start=%d body=%s", start.Code, start.Body.String())
	}
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := location.Query().Get("state")
	previousCookie := &http.Cookie{Name: "lcm_oidc_state", Value: state, Path: "/api/v1/auth/oidc/"}
	callback := "/api/v1/auth/oidc/" + provider.ID + "/callback?state=" + url.QueryEscape(state) + "&code=provider-code"
	w := adminRequest(t, f.raw, http.MethodGet, callback, nil, []*http.Cookie{previousCookie}, nil)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "OIDC authentication failed") {
		t.Fatalf("previous state cookie callback=%d body=%s", w.Code, w.Body.String())
	}
}
