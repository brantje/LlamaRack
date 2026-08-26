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
	var enabled int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil { t.Fatal(err) }
	if enabled != 1 { t.Fatalf("foreign_keys = %d", enabled) }
	if _, err := db.ExecContext(ctx, "INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES('x',999,'hash',1)"); err == nil {
		t.Fatal("expected foreign key violation")
	}
}

func TestMigrateIsIdempotentAndClosedDBFails(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil { t.Fatal(err) }
	if err := migrate(ctx, db); err != nil { t.Fatal(err) }
	if err := migrate(ctx, db); err != nil { t.Fatalf("second migrate: %v", err) }
	if err := db.Close(); err != nil { t.Fatal(err) }
	if err := migrate(ctx, db); err == nil { t.Fatal("expected migrate on closed DB to fail") }
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
