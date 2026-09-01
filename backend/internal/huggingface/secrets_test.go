package huggingface

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestSecretStoreRoundTripAndKeyReuse(t *testing.T) {
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
	status, err := store.TokenStatus(ctx)
	if err != nil || status.Configured {
		t.Fatalf("initial status = %+v %v", status, err)
	}
	if token, err := store.GetToken(ctx); err != nil || token != "" {
		t.Fatalf("initial token = %q %v", token, err)
	}
	if err := store.SetToken(ctx, "  hf_abcdef123456  "); err != nil {
		t.Fatal(err)
	}
	status, err = store.TokenStatus(ctx)
	if err != nil || !status.Configured || status.Prefix != "hf_abc" {
		t.Fatalf("status = %+v %v", status, err)
	}
	if token, err := store.GetToken(ctx); err != nil || token != "hf_abcdef123456" {
		t.Fatalf("token = %q %v", token, err)
	}

	keyPath := filepath.Join(root, "provider-secrets.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key permissions = %o", info.Mode().Perm())
	}
	second, err := NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if token, err := second.GetToken(ctx); err != nil || token != "hf_abcdef123456" {
		t.Fatalf("reopened token = %q %v", token, err)
	}
	if err := second.DeleteToken(ctx); err != nil {
		t.Fatal(err)
	}
	status, err = second.TokenStatus(ctx)
	if err != nil || status.Configured {
		t.Fatalf("deleted status = %+v %v", status, err)
	}
}

func TestSecretStoreValidationAndBadKey(t *testing.T) {
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
	if err := store.SetToken(ctx, "   "); err == nil {
		t.Fatal("expected empty token error")
	}

	badRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(badRoot, "provider-secrets.key"), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSecretStore(db, badRoot); err == nil {
		t.Fatal("expected invalid key length error")
	}
}

func TestSecretStoreDetectsTamperedCiphertext(t *testing.T) {
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
	if err := store.SetToken(ctx, "hf_secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE provider_secrets SET ciphertext=? WHERE name=?", []byte("broken"), tokenSecretName); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetToken(ctx); err == nil {
		t.Fatal("expected decrypt error")
	}
}
