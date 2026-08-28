package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type configurableOIDCSecrets struct {
	base          *memoryOIDCSecrets
	getErr        error
	configuredErr error
}

func (s *configurableOIDCSecrets) GetSecret(ctx context.Context, name string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	return s.base.GetSecret(ctx, name)
}

func (s *configurableOIDCSecrets) SetSecret(ctx context.Context, name, value string) error {
	return s.base.SetSecret(ctx, name, value)
}

func (s *configurableOIDCSecrets) DeleteSecret(ctx context.Context, name string) error {
	return s.base.DeleteSecret(ctx, name)
}

func (s *configurableOIDCSecrets) SecretConfigured(ctx context.Context, name string) (bool, error) {
	if s.configuredErr != nil {
		return false, s.configuredErr
	}
	return s.base.SecretConfigured(ctx, name)
}

func TestPersistentSigningKeyAndBearerErrorPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", signingKeyFilename)

	created, err := loadOrCreateEd25519Key(path)
	if err != nil || len(created) != ed25519.PrivateKeySize {
		t.Fatalf("create signing key len=%d err=%v", len(created), err)
	}
	loaded, err := loadOrCreateEd25519Key(path)
	if err != nil || !created.Equal(loaded) {
		t.Fatalf("reload signing key err=%v", err)
	}

	_, fullPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fullPrivate, 0o600); err != nil {
		t.Fatal(err)
	}
	loadedPrivate, err := loadOrCreateEd25519Key(path)
	if err != nil || !fullPrivate.Equal(loadedPrivate) {
		t.Fatalf("full private key reload err=%v", err)
	}

	if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateEd25519Key(path); err == nil {
		t.Fatal("invalid signing key length should fail")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateEd25519Key(path); err == nil {
		t.Fatal("directory in place of signing key should fail")
	}

	s := testService(t)
	if err := s.UsePersistentSigningKey(filepath.Join(root, "service")); err != nil {
		t.Fatal(err)
	}
	user, err := s.Bootstrap(t.Context(), "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBearerSession(t.Context(), User{}, "", ""); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("invalid bearer user err=%v", err)
	}

	s.mu.Lock()
	s.jwtPrivate = nil
	s.mu.Unlock()
	if _, err := s.CreateBearerSession(t.Context(), user, "", ""); err == nil {
		t.Fatal("missing signing key should fail session creation")
	}
	s.mu.Lock()
	s.jwtPublic = nil
	s.mu.Unlock()
	if _, err := s.parseManagementToken("not-a-token"); err == nil {
		t.Fatal("missing verification key should reject token")
	}
}

func TestOIDCSecretProviderAndIdentityErrorPaths(t *testing.T) {
	f := newOIDCFixture(t)
	ctx := t.Context()
	idp := newTestOIDCProvider(t)
	f.manager.client = idp.server.Client()
	secret := "client-secret"

	provider, err := f.manager.CreateProvider(ctx, idp.input(&secret))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.secrets.DeleteSecret(ctx, oidcSecretName(provider.ID)); err != nil {
		t.Fatal(err)
	}
	failed, err := f.manager.TestProvider(ctx, provider.ID)
	if err == nil || failed.LastTestSucceeded || failed.LastTestedAt == nil {
		t.Fatalf("missing-secret provider test=%+v err=%v", failed, err)
	}
	if _, err := f.manager.Start(ctx, provider.ID, false, "", "", "https://manager.example.test"); err == nil {
		t.Fatal("OIDC start without provider secret should fail")
	}

	if err := f.secrets.SetSecret(ctx, oidcSecretName(provider.ID), secret); err != nil {
		t.Fatal(err)
	}
	wrapped := &configurableOIDCSecrets{base: f.secrets, configuredErr: errors.New("secret status unavailable")}
	f.manager.secrets = wrapped
	if _, err := f.manager.TestProvider(ctx, provider.ID); err == nil {
		t.Fatal("secret status failure should fail provider test")
	}
	wrapped.configuredErr = nil
	wrapped.getErr = errors.New("secret read unavailable")
	if _, err := f.manager.Start(ctx, provider.ID, false, "", "", "https://manager.example.test"); err == nil {
		t.Fatal("secret read failure should fail OIDC start")
	}
	f.manager.secrets = f.secrets

	disabledInput := idp.input(nil)
	disabledInput.Enabled = false
	disabled, err := f.manager.UpdateProvider(ctx, provider.ID, disabledInput)
	if err != nil || disabled.Enabled {
		t.Fatalf("disable provider=%+v err=%v", disabled, err)
	}
	if _, err := f.manager.Start(ctx, provider.ID, false, "", "", "https://manager.example.test"); err == nil {
		t.Fatal("disabled provider should not start")
	}

	enabledInput := idp.input(nil)
	provider, err = f.manager.UpdateProvider(ctx, provider.ID, enabledInput)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := f.auth.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := f.manager.LinkIdentity(ctx, admin.ID, provider.ID, "", "disabled-subject")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.SetUserEnabled(ctx, admin.ID, false); !errors.Is(err, ErrLastEnabledUser) {
		t.Fatalf("expected last-user guard, got %v", err)
	}
	second, err := f.auth.CreateUser(ctx, "second", "another-correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.auth.SetUserEnabled(ctx, admin.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.resolveIdentity(ctx, provider, identity.Subject, "ignored"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("disabled linked user err=%v", err)
	}
	if _, err := f.manager.LinkIdentity(ctx, 999999, provider.ID, "", "missing-user"); err == nil {
		t.Fatal("identity link to missing user should fail")
	}
	if resultSessionID("not-a-token", f.auth) != "" {
		t.Fatal("invalid bearer token should not yield a session id")
	}

	if err := f.auth.SetUserEnabled(ctx, admin.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.auth.SetUserEnabled(ctx, second.ID, false); err != nil {
		t.Fatal(err)
	}

	f.manager.mu.Lock()
	f.manager.transactions["wrong-provider"] = oidcTransaction{ProviderID: "other", ExpiresAt: time.Now().Add(time.Minute)}
	f.manager.transactions["blank-code"] = oidcTransaction{ProviderID: provider.ID, ExpiresAt: time.Now().Add(time.Minute)}
	f.manager.mu.Unlock()
	if _, err := f.manager.CompleteCallback(ctx, provider.ID, "wrong-provider", "code", "https://manager.example.test"); err == nil {
		t.Fatal("provider-mismatched OIDC state should fail")
	}
	if _, err := f.manager.CompleteCallback(ctx, provider.ID, "blank-code", " ", "https://manager.example.test"); err == nil {
		t.Fatal("blank OIDC callback code should fail")
	}
}
