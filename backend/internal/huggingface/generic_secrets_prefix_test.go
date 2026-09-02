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
}
