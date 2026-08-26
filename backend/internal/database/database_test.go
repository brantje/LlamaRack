package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchemaAndEnablesForeignKeys(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "manager.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, table := range []string{"users", "sessions", "api_keys", "models", "model_options", "instances"} {
		var name string
		if err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	var artifacts int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='artifacts'").Scan(&artifacts); err != nil {
		t.Fatal(err)
	}
	if artifacts != 0 {
		t.Fatal("artifacts table should not exist")
	}
	for _, column := range []string{"name", "gguf_path", "total_bytes", "quantization", "autoload_enabled", "eviction_enabled", "idle_unload_seconds"} {
		var count int
		rows, err := db.QueryContext(ctx, "PRAGMA table_info(models)")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var cid, notNull, pk int
			var name, typ string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if name == column {
				count++
			}
		}
		rows.Close()
		if count != 1 {
			t.Fatalf("models.%s missing", column)
		}
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO models(id,public_id,name,gguf_path,total_bytes) VALUES('m1','one','One','same.gguf',1)"); err != nil {
		t.Fatal(err)
	}
	var evictionEnabled, idleUnloadSeconds int
	if err := db.QueryRowContext(ctx, "SELECT eviction_enabled,idle_unload_seconds FROM models WHERE id='m1'").Scan(&evictionEnabled, &idleUnloadSeconds); err != nil {
		t.Fatal(err)
	}
	if evictionEnabled != 1 || idleUnloadSeconds != 0 {
		t.Fatalf("unexpected eviction defaults enabled=%d idle=%d", evictionEnabled, idleUnloadSeconds)
	}
	if _, err := db.ExecContext(ctx, "UPDATE models SET idle_unload_seconds=-1 WHERE id='m1'"); err == nil {
		t.Fatal("expected non-negative idle timeout constraint")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO models(id,public_id,name,gguf_path,total_bytes) VALUES('m2','two','Two','same.gguf',1)"); err == nil {
		t.Fatal("expected unique GGUF path constraint")
	}
	var enabled int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatalf("foreign_keys = %d", enabled)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES('x',999,'hash',1)"); err == nil {
		t.Fatal("expected foreign key violation")
	}
}

func TestInitializeSchemaIsIdempotentAndClosedDBFails(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := initializeSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := initializeSchema(ctx, db); err != nil {
		t.Fatalf("second initialize: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := initializeSchema(ctx, db); err == nil {
		t.Fatal("expected initializeSchema on closed DB to fail")
	}
}

func TestOpenFailsWhenParentCannotBeCreated(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), filepath.Join(blocker, "manager.db")); err == nil {
		t.Fatal("expected mkdir failure")
	}
}

func TestOpenFailsWhenContextCanceledDuringPragma(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if db, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db")); err == nil {
		_ = db.Close()
		t.Fatal("expected canceled context error")
	}
}
