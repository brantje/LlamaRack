package litellm

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/modelimports"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestLiteLLMStoreSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.db")
	runLiteLLMStoreContract(t, func() database.Store {
		db, err := database.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		return db
	})
}

func TestLiteLLMStorePostgres(t *testing.T) {
	dsn := liteLLMPostgresDSN(t)
	runLiteLLMStoreContract(t, func() database.Store {
		db, err := database.OpenConfigured(context.Background(), "", dsn)
		if err != nil {
			t.Fatal(err)
		}
		return db
	})
}

func runLiteLLMStoreContract(t *testing.T, open func() database.Store) {
	t.Helper()
	ctx := context.Background()
	db := open()
	store := NewLiteLLMStore(db)

	if value, found, err := store.Setting(ctx, "litellm-contract"); err != nil || found || value != "" {
		t.Fatalf("missing setting: value=%q found=%v err=%v", value, found, err)
	}
	if err := store.SetSetting(ctx, "litellm-contract", "first"); err != nil {
		t.Fatal(err)
	}
	if value, found, err := store.Setting(ctx, "litellm-contract"); err != nil || !found || value != "first" {
		t.Fatalf("loaded setting: value=%q found=%v err=%v", value, found, err)
	}
	if err := store.SetSetting(ctx, "litellm-contract", "updated"); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO models(id,slug,name,gguf_path,total_bytes,context_length) VALUES(?,?,?,?,?,?)`,
		"litellm-model", "litellm-model", "LiteLLM model", "litellm/model.gguf", 1, 0); err != nil {
		t.Fatal(err)
	}
	instanceService := instances.New(db)
	enabled, disabled := true, false
	visible, err := instanceService.Create(ctx, instances.CreateInput{ModelID: "litellm-model", Name: "Visible", Slug: "visible", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := instanceService.Create(ctx, instances.CreateInput{ModelID: "litellm-model", Name: "Disabled", Slug: "disabled", Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	downloading, err := instanceService.Create(ctx, instances.CreateInput{ModelID: "litellm-model", Name: "Downloading", Slug: "downloading", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO download_jobs(
		id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,0,0,0,'',unixepoch(),unixepoch())`,
		"litellm-job", "huggingface", "acme/demo", "rev", "artifact", "demo.gguf", "", downloads.StateDownloading); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_imports(
		id,job_id,model_id,instance_id,owns_model,start_when_ready,state,error,start_attempted,created_at,updated_at
	) VALUES(?,?,?,?,0,0,?,'',0,unixepoch(),unixepoch())`,
		"litellm-import", "litellm-job", "litellm-model", downloading.ID, modelimports.StateDownloading); err != nil {
		t.Fatal(err)
	}
	items, err := store.SyncInstances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != visible.ID || items[0].Slug != visible.Slug {
		t.Fatalf("sync instances=%+v", items)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = open()
	store = NewLiteLLMStore(db)
	defer db.Close()
	if value, found, err := store.Setting(ctx, "litellm-contract"); err != nil || !found || value != "updated" {
		t.Fatalf("reopened setting: value=%q found=%v err=%v", value, found, err)
	}
	if err := store.DeleteSetting(ctx, "litellm-contract"); err != nil {
		t.Fatal(err)
	}
	if value, found, err := store.Setting(ctx, "litellm-contract"); err != nil || found || value != "" {
		t.Fatalf("deleted setting: value=%q found=%v err=%v", value, found, err)
	}
}

func liteLLMPostgresDSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv("LLAMARACK_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("LLAMARACK_TEST_POSTGRES_URL is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "llamarack_litellm_" + hex.EncodeToString(random[:])
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
