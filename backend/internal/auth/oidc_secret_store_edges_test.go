package auth

import (
	"context"
	"errors"
	"testing"
)

type failingOIDCSecretStore struct {
	base      *memoryOIDCSecrets
	setErr    error
	deleteErr error
}

func (s *failingOIDCSecretStore) GetSecret(ctx context.Context, name string) (string, error) {
	return s.base.GetSecret(ctx, name)
}

func (s *failingOIDCSecretStore) SetSecret(ctx context.Context, name, value string) error {
	if s.setErr != nil {
		return s.setErr
	}
	return s.base.SetSecret(ctx, name, value)
}

func (s *failingOIDCSecretStore) DeleteSecret(ctx context.Context, name string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.base.DeleteSecret(ctx, name)
}

func (s *failingOIDCSecretStore) SecretConfigured(ctx context.Context, name string) (bool, error) {
	return s.base.SecretConfigured(ctx, name)
}

func TestOIDCSecretStoreMutationFailures(t *testing.T) {
	f := newOIDCFixture(t)
	idp := newTestOIDCProvider(t)
	secret := "client-secret"
	store := &failingOIDCSecretStore{base: f.secrets}
	f.manager.secrets = store

	store.setErr = errors.New("secret write failed")
	if _, err := f.manager.CreateProvider(t.Context(), idp.input(&secret)); err == nil {
		t.Fatal("provider creation should fail when secret persistence fails")
	}
	providers, err := f.manager.ListProviders(t.Context())
	if err != nil || len(providers) != 0 {
		t.Fatalf("failed provider create must roll back row: providers=%+v err=%v", providers, err)
	}

	store.setErr = nil
	provider, err := f.manager.CreateProvider(t.Context(), idp.input(&secret))
	if err != nil {
		t.Fatal(err)
	}
	replacement := "replacement-secret"
	input := idp.input(&replacement)
	store.setErr = errors.New("secret replace failed")
	if _, err := f.manager.UpdateProvider(t.Context(), provider.ID, input); err == nil {
		t.Fatal("provider update should report secret persistence failure")
	}

	store.setErr = nil
	store.deleteErr = errors.New("secret delete failed")
	if err := f.manager.DeleteProvider(t.Context(), provider.ID); err == nil {
		t.Fatal("provider deletion should report secret deletion failure")
	}
	if _, err := f.manager.GetProvider(t.Context(), provider.ID); err == nil {
		t.Fatal("provider row should already be deleted when secret deletion fails")
	}
}
