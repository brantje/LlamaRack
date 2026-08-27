package huggingface

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/database"
)

func TestSecretStoreDatabaseErrors(t *testing.T) {
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
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := store.GetToken(ctx); err == nil {
		t.Fatal("expected GetToken database error")
	}
	if err := store.SetToken(ctx, "hf_test"); err == nil {
		t.Fatal("expected SetToken database error")
	}
	if err := store.DeleteToken(ctx); err == nil {
		t.Fatal("expected DeleteToken database error")
	}
	if _, err := store.TokenStatus(ctx); err == nil {
		t.Fatal("expected TokenStatus database error")
	}
}

func TestLoadOrCreateKeyRejectsUnreadableKeyPath(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "provider-secrets.key")
	if err := os.Mkdir(keyPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateKey(keyPath); err == nil {
		t.Fatal("expected key path read error")
	}
}
