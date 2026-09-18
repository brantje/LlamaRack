package database

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenStoreUsesGORMAdapterWithoutChangingSQLiteSemantics(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if store.Dialect() != DialectSQLite {
		t.Fatalf("dialect=%q", store.Dialect())
	}
	if _, err := store.ExecContext(ctx, "INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)", "gorm-store", "ok", 1); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := store.QueryRowContext(ctx, "SELECT setting_value FROM manager_settings WHERE setting_key=?", "gorm-store").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "ok" {
		t.Fatalf("value=%q", value)
	}

	tx, err := Begin(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE manager_settings SET setting_value=? WHERE setting_key=?", "tx", "gorm-store"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.QueryRowContext(ctx, "SELECT setting_value FROM manager_settings WHERE setting_key=?", "gorm-store").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "tx" {
		t.Fatalf("transaction value=%q", value)
	}
}

func TestOpenConfiguredRejectsUnsupportedDatabaseURL(t *testing.T) {
	if _, err := OpenConfigured(context.Background(), filepath.Join(t.TempDir(), "manager.db"), "mysql://user:secret@db/llamarack"); err == nil {
		t.Fatal("expected unsupported database URL error")
	}
}

func TestRedactDatabaseErrorDoesNotExposePasswordOrDSN(t *testing.T) {
	dsn := "postgres://user:super-secret@db.example/llamarack?sslmode=require"
	err := redactDatabaseError("connect PostgreSQL database", dsn, errors.New("failed for "+dsn+" password=super-secret"))
	if got := err.Error(); strings.Contains(got, dsn) || strings.Contains(got, "super-secret") {
		t.Fatalf("database error leaked credentials: %q", got)
	}
}
