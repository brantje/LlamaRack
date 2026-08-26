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
	if err != nil { t.Fatal(err) }
	defer db.Close()

	for _, table := range []string{"users", "sessions", "api_keys", "artifacts", "models", "model_options", "instances"} {
		var name string
		if err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	if ok, err := columnExists(ctx, db, "models", "autoload_enabled"); err != nil || !ok {
		t.Fatalf("autoload_enabled missing: ok=%v err=%v", ok, err)
	}
	var enabled int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil { t.Fatal(err) }
	if enabled != 1 { t.Fatalf("foreign_keys = %d", enabled) }
	if _, err := db.ExecContext(ctx, "INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES('x',999,'hash',1)"); err == nil {
		t.Fatal("expected foreign key violation")
	}
}

func TestMigrateUpgradesLegacyAutoloadColumn(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.sqlite"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	_, err = db.ExecContext(ctx, `
CREATE TABLE models (
 id TEXT PRIMARY KEY,
 public_id TEXT NOT NULL UNIQUE,
 display_name TEXT,
 artifact_id TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1,
 autoload INTEGER NOT NULL DEFAULT 1,
 always_on INTEGER NOT NULL DEFAULT 0,
 priority TEXT NOT NULL DEFAULT 'normal',
 routing_policy TEXT NOT NULL DEFAULT 'least_active',
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
INSERT INTO models(id,public_id,artifact_id,autoload) VALUES('m1','legacy','a1',0);
`)
	if err != nil { t.Fatal(err) }
	if err := migrate(ctx, db); err != nil { t.Fatal(err) }
	if ok, err := columnExists(ctx, db, "models", "autoload_enabled"); err != nil || !ok {
		t.Fatalf("autoload_enabled missing after migration: ok=%v err=%v", ok, err)
	}
	var autoload int
	if err := db.QueryRowContext(ctx, "SELECT autoload_enabled FROM models WHERE id='m1'").Scan(&autoload); err != nil { t.Fatal(err) }
	if autoload != 0 { t.Fatalf("autoload_enabled=%d, want legacy value 0", autoload) }
	if err := migrate(ctx, db); err != nil { t.Fatalf("second migrate: %v", err) }
}

func TestMigrateIsIdempotentAndClosedDBFails(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil { t.Fatal(err) }
	if err := migrate(ctx, db); err != nil { t.Fatal(err) }
	if err := migrate(ctx, db); err != nil { t.Fatalf("second migrate: %v", err) }
	if ok, err := columnExists(ctx, db, "models", "missing_column"); err != nil || ok {
		t.Fatalf("missing column check: ok=%v err=%v", ok, err)
	}
	if err := db.Close(); err != nil { t.Fatal(err) }
	if err := migrate(ctx, db); err == nil { t.Fatal("expected migrate on closed DB to fail") }
	if _, err := columnExists(ctx, db, "models", "autoload_enabled"); err == nil { t.Fatal("expected columnExists on closed DB to fail") }
}

func TestOpenFailsWhenParentCannotBeCreated(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil { t.Fatal(err) }
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
