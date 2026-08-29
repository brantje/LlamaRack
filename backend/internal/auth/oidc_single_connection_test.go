package auth_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

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
}
