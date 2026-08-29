package huggingface

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/database"
)

func TestGenericSecretsDoNotPersistPlaintextPrefixes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}

	const name = "oidc_provider:test:client_secret"
	const secret = "super-secret-client-credential"
	if err := store.SetSecret(ctx, name, secret); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetSecret(ctx, name); err != nil || got != secret {
		t.Fatalf("generic secret round trip=%q err=%v", got, err)
	}
	configured, err := store.SecretConfigured(ctx, name)
	if err != nil || !configured {
		t.Fatalf("configured=%v err=%v", configured, err)
	}
	var prefix string
	if err := db.QueryRowContext(ctx, "SELECT prefix FROM provider_secrets WHERE name=?", name).Scan(&prefix); err != nil {
		t.Fatal(err)
	}
	if prefix != "" {
		t.Fatalf("opaque secret persisted plaintext prefix %q", prefix)
	}

	if err := store.DeleteSecret(ctx, name); err != nil {
		t.Fatal(err)
	}
	configured, err = store.SecretConfigured(ctx, name)
	if err != nil || configured {
		t.Fatalf("configured after delete=%v err=%v", configured, err)
	}
}
