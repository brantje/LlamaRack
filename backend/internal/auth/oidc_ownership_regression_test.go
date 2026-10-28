package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// Self-service unlinking must not let one authenticated user detach another
// user's external identity. Failure must not destroy the legitimate link.
func TestOIDCUnlinkOwnIdentityEnforcesOwnership(t *testing.T) {
	f := newOIDCFixture(t)
	ctx := t.Context()
	providerServer := newTestOIDCProvider(t)
	secret := "client-secret"
	provider, err := f.manager.CreateProvider(ctx, providerServer.input(&secret))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := f.auth.Bootstrap(ctx, "owner", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := f.auth.CreateUser(ctx, "stranger", "another-correct-password")
	if err != nil {
		t.Fatal(err)
	}
	linked, err := f.manager.LinkIdentity(ctx, owner.ID, provider.ID, "", "owner-subject")
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		userID int64
		id string
	}{
		"another user": {stranger.ID, linked.ID},
		"invalid user": {0, linked.ID},
		"empty identity": {owner.ID, "  "},
		"unknown identity": {owner.ID, "does-not-exist"},
	} {
		t.Run(name, func(t *testing.T) {
			err := f.manager.UnlinkOwnIdentity(ctx, tc.userID, tc.id)
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("expected not found without revealing ownership; got %v", err)
			}
			identities, err := f.manager.ListIdentities(ctx, owner.ID)
			if err != nil || len(identities) != 1 || identities[0].ID != linked.ID {
				t.Fatalf("failed unlink affected the owner's identity: %+v, err=%v", identities, err)
			}
		})
	}
	if err := f.manager.UnlinkOwnIdentity(ctx, owner.ID, " "+linked.ID+" "); err != nil {
		t.Fatalf("owner must be able to unlink with normalized ID: %v", err)
	}
	identities, err := f.manager.ListIdentities(ctx, owner.ID)
	if err != nil || len(identities) != 0 {
		t.Fatalf("identity still linked: %+v, err=%v", identities, err)
	}
	if err := f.manager.UnlinkOwnIdentity(ctx, owner.ID, linked.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("repeated unlink should be not found: %v", err)
	}
}

func TestTrustedInferencePrincipalContextDoesNotTrustIncompleteOrExternalValues(t *testing.T) {
	plain := context.Background()
	if _, ok := TrustedInferencePrincipalFromContext(plain); ok || trustedInferenceContext(plain) {
		t.Fatal("unmarked request was trusted")
	}
	// A forged string key is not the private typed context key.
	forged := context.WithValue(plain, "trustedInferenceContextKey", TrustedInferencePrincipal{Kind: "user", ID: "7"})
	if _, ok := TrustedInferencePrincipalFromContext(forged); ok || trustedInferenceContext(forged) {
		t.Fatal("forged context key was accepted")
	}
	for _, principal := range []TrustedInferencePrincipal{{Kind: "", ID: "7"}, {Kind: "user", ID: ""}} {
		marked := WithTrustedInferenceContext(plain, principal)
		if _, ok := TrustedInferencePrincipalFromContext(marked); ok {
			t.Fatalf("incomplete principal was accepted: %+v", principal)
		}
	}
	want := TrustedInferencePrincipal{Kind: "user", ID: "7"}
	marked := WithTrustedInferenceContext(plain, want)
	got, ok := TrustedInferencePrincipalFromContext(marked)
	if !ok || got != want || !trustedInferenceContext(marked) {
		t.Fatalf("management principal not preserved: %+v ok=%v", got, ok)
	}
	if _, ok := TrustedInferencePrincipalFromContext(plain); ok {
		t.Fatal("child context marker leaked to parent")
	}
}
