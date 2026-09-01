package auth_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/settings"
)

type failingConfiguredSecretStore struct {
	*huggingface.SecretStore
	err error
}

func (s failingConfiguredSecretStore) SecretConfigured(context.Context, string) (bool, error) {
	return false, s.err
}

func TestOIDCListProvidersWithDatabaseBackedSecretStore(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.Open(t.Context(), filepath.Join(dataDir, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	managerSettings := settings.New(db, settings.Defaults{
		SessionLifetime:   time.Hour,
		StartupTimeout:    time.Minute,
		AlwaysOnReconcile: time.Second,
	})
	authService := auth.New(db, time.Hour)
	secretStore, err := huggingface.NewSecretStore(db, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	manager := auth.NewOIDCManager(authService, managerSettings, secretStore)

	secret := "client-secret"
	created, err := manager.CreateProvider(t.Context(), auth.OIDCProviderInput{
		Name:         "Authentik",
		Enabled:      true,
		Issuer:       "https://auth.example.test/application/o/manager",
		ClientID:     "manager-client",
		ClientSecret: &secret,
		Scopes:       []string{"openid", "profile", "email"},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	providers, err := manager.ListProviders(ctx)
	if err != nil {
		t.Fatalf("list providers: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers=%d want=1", len(providers))
	}
	if providers[0].ID != created.ID {
		t.Fatalf("provider id=%q want=%q", providers[0].ID, created.ID)
	}
	if !providers[0].SecretConfigured {
		t.Fatal("provider should report configured client secret")
	}

	statusErr := errors.New("secret status failed")
	failingManager := auth.NewOIDCManager(authService, managerSettings, failingConfiguredSecretStore{SecretStore: secretStore, err: statusErr})
	if _, err := failingManager.ListProviders(t.Context()); !errors.Is(err, statusErr) {
		t.Fatalf("list providers secret status error=%v want=%v", err, statusErr)
	}

	canceledCtx, cancelList := context.WithCancel(t.Context())
	cancelList()
	if _, err := manager.ListProviders(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("list providers canceled context error=%v want=%v", err, context.Canceled)
	}

	if _, err := db.ExecContext(t.Context(), "UPDATE oidc_providers SET created_at='not-an-integer' WHERE id=?", created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ListProviders(t.Context()); err == nil {
		t.Fatal("list providers should fail when a provider row cannot be scanned")
	}
}
