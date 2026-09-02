package huggingface

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestSetSecretWithPrefixStoresDisplayPrefix(t *testing.T) {
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
	if err := store.SetSecretWithPrefix(ctx, "litellm_proxy_api_key", "abcdefghijklmnop"); err != nil {
		t.Fatal(err)
	}
	status, err := store.SecretStatus(ctx, "litellm_proxy_api_key")
	if err != nil || !status.Configured || status.Prefix != "abcdefgh" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if got, err := store.GetSecret(ctx, "litellm_proxy_api_key"); err != nil || got != "abcdefghijklmnop" {
		t.Fatalf("secret=%q err=%v", got, err)
	}
	if _, err := store.GetSecret(ctx, ""); err == nil {
		t.Fatal("expected empty name error")
	}
	if got, err := store.GetSecret(ctx, "missing"); err != nil || got != "" {
		t.Fatalf("missing secret=%q err=%v", got, err)
	}
	missing, err := store.SecretStatus(ctx, "missing")
	if err != nil || missing.Configured {
		t.Fatalf("missing status=%+v err=%v", missing, err)
	}
	configured, err := store.SecretConfigured(ctx, "missing")
	if err != nil || configured {
		t.Fatalf("configured=%v err=%v", configured, err)
	}
	if err := store.SetSecret(ctx, "", "value"); err == nil {
		t.Fatal("expected empty set error")
	}
}

func TestGenericSecretsErrorPaths(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSecret(ctx, "generic", "value"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE provider_secrets SET ciphertext=?, nonce=? WHERE name='generic'`, []byte("not-valid-ciphertext"), make([]byte, store.aead.NonceSize())); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSecret(ctx, "generic"); err == nil {
		t.Fatal("expected decrypt error")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSecret(ctx, "generic"); err == nil {
		t.Fatal("expected closed db get error")
	}
	if _, err := store.SecretStatus(ctx, "generic"); err == nil {
		t.Fatal("expected closed db status error")
	}
	if _, err := store.SecretConfigured(ctx, "generic"); err == nil {
		t.Fatal("expected closed db configured error")
	}
	if err := store.DeleteSecret(ctx, "generic"); err == nil {
		t.Fatal("expected closed db delete error")
	}
	if err := store.SetSecret(ctx, "other", "value"); err == nil {
		t.Fatal("expected closed db set error")
	}
}
