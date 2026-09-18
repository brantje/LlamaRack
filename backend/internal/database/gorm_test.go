package database

import (
	"context"
	"path/filepath"
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
	result, err := store.ExecContext(ctx, "INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)", "gorm-store", "ok", 1)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := result.LastInsertId(); err != nil || id <= 0 {
		t.Fatalf("last insert id=%d err=%v", id, err)
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
