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

func TestGORMAdapterQueryRollbackAndResultSemantics(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.ExecContext(ctx, "INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)", "one", "1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecContext(ctx, "INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)", "two", "2", 2); err != nil {
		t.Fatal(err)
	}
	rows, err := store.QueryContext(ctx, "SELECT setting_key FROM manager_settings WHERE setting_key IN (?,?) ORDER BY setting_key", "one", "two")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "one" || keys[1] != "two" {
		t.Fatalf("keys=%v", keys)
	}

	tx, err := Begin(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE manager_settings SET setting_value=? WHERE setting_key=?", "rolled-back", "one"); err != nil {
		t.Fatal(err)
	}
	txRows, err := tx.QueryContext(ctx, "SELECT setting_key FROM manager_settings WHERE setting_key=?", "one")
	if err != nil {
		t.Fatal(err)
	}
	if !txRows.Next() {
		txRows.Close()
		t.Fatal("transaction query returned no row")
	}
	if err := txRows.Close(); err != nil {
		t.Fatal(err)
	}
	var inTx string
	if err := tx.QueryRowContext(ctx, "SELECT setting_value FROM manager_settings WHERE setting_key=?", "one").Scan(&inTx); err != nil {
		t.Fatal(err)
	}
	if inTx != "rolled-back" {
		t.Fatalf("transaction value=%q", inTx)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var persisted string
	if err := store.QueryRowContext(ctx, "SELECT setting_value FROM manager_settings WHERE setting_key=?", "one").Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != "1" {
		t.Fatalf("rollback persisted value=%q", persisted)
	}

	r := result{rows: 3}
	if got, err := r.RowsAffected(); err != nil || got != 3 {
		t.Fatalf("RowsAffected=%d err=%v", got, err)
	}
	if _, err := r.LastInsertId(); err == nil {
		t.Fatal("LastInsertId unexpectedly succeeded")
	}
}

func TestGORMAdapterNilSafeHelpers(t *testing.T) {
	var store *Connection
	if got := store.Dialect(); got != "" {
		t.Fatalf("nil dialect=%q", got)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("nil close err=%v", err)
	}
	if _, err := store.ExecContext(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("nil ExecContext succeeded")
	}
	if _, err := store.QueryContext(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("nil QueryContext succeeded")
	}
	if err := redactDatabaseError("prefix", "postgres://example/db", nil); err != nil {
		t.Fatalf("nil database error=%v", err)
	}
}
