package api

import (
	"net/http"
	"testing"

	"github.com/brantje/llamarack/backend/internal/auth"
)

// The /me endpoint is intentionally narrower than the administrative unlink:
// an authenticated administrator still cannot unlink somebody else's identity
// through /me, even when they know the identity's ID.
func TestOIDCSelfUnlinkRejectsForeignIdentity(t *testing.T) {
	f := newAPIOIDCFixture(t)
	ctx := t.Context()
	other, err := f.auth.CreateUser(ctx, "other-user", "another-correct-password")
	if err != nil {
		t.Fatal(err)
	}
	secret := "client-secret"
	provider, err := f.oidc.CreateProvider(ctx, auth.OIDCProviderInput{
		Name: "Primary", Enabled: true, Issuer: f.idp.URL,
		ClientID: "manager", ClientSecret: &secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	linked, err := f.oidc.LinkIdentity(ctx, other.ID, provider.ID, "", "other-subject")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/me/identities/" + linked.ID
	stranger := adminRequest(t, f.secured, http.MethodDelete, path, nil, nil, f.headers())
	if stranger.Code != http.StatusNotFound {
		t.Fatalf("foreign identity unlink returned %d: %s", stranger.Code, stranger.Body.String())
	}
	identities, err := f.oidc.ListIdentities(ctx, other.ID)
	if err != nil || len(identities) != 1 || identities[0].ID != linked.ID {
		t.Fatalf("foreign identity was modified: %+v err=%v", identities, err)
	}
	unauthorized := adminRequest(t, f.secured, http.MethodDelete, path, nil, nil, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized unlink returned %d", unauthorized.Code)
	}
	login, err := f.auth.LoginBearerWithMetadata(ctx, "other-user", "another-correct-password", "127.0.0.1", "ownership-test")
	if err != nil {
		t.Fatal(err)
	}
	ownerHeaders := map[string]string{"Authorization": "Bearer " + login.AccessToken}
	own := adminRequest(t, f.secured, http.MethodDelete, path, nil, nil, ownerHeaders)
	if own.Code != http.StatusNoContent {
		t.Fatalf("owner's unlink returned %d: %s", own.Code, own.Body.String())
	}
	again := adminRequest(t, f.secured, http.MethodDelete, path, nil, nil, ownerHeaders)
	if again.Code != http.StatusNotFound {
		t.Fatalf("repeated unlink returned %d: %s", again.Code, again.Body.String())
	}
}
