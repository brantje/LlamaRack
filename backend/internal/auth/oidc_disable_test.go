package auth

import "testing"

func TestOIDCDisableClearsSuccessfulTestState(t *testing.T) {
	f := newOIDCFixture(t)
	providerServer := newTestOIDCProvider(t)
	secret := "client-secret"

	provider, err := f.manager.CreateProvider(t.Context(), providerServer.input(&secret))
	if err != nil {
		t.Fatal(err)
	}
	provider, err = f.manager.TestProvider(t.Context(), provider.ID)
	if err != nil || !provider.LastTestSucceeded || provider.LastTestedAt == nil {
		t.Fatalf("tested provider=%+v err=%v", provider, err)
	}

	input := providerServer.input(nil)
	input.Enabled = false
	provider, err = f.manager.UpdateProvider(t.Context(), provider.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if provider.Enabled || provider.LastTestSucceeded || provider.LastTestedAt != nil {
		t.Fatalf("disabled provider retained stale test state: %+v", provider)
	}
}
