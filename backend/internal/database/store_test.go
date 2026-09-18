package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBeginCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = db.Close() })

	tx, err := Begin(ctx, db)
	if err != nil { t.Fatal(err) }
	if _, err := tx.ExecContext(ctx, "INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)", "commit-test", "yes", 1); err != nil { t.Fatal(err) }
	if err := tx.Commit(); err != nil { t.Fatal(err) }
	var value string
	if err := db.QueryRowContext(ctx, "SELECT setting_value FROM manager_settings WHERE setting_key=?", "commit-test").Scan(&value); err != nil { t.Fatal(err) }
	if value != "yes" { t.Fatalf("value=%q", value) }

	tx, err = Begin(ctx, db)
	if err != nil { t.Fatal(err) }
	if _, err := tx.ExecContext(ctx, "INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)", "rollback-test", "no", 1); err != nil { t.Fatal(err) }
	if err := tx.Rollback(); err != nil { t.Fatal(err) }
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM manager_settings WHERE setting_key=?", "rollback-test").Scan(&count); err != nil { t.Fatal(err) }
	if count != 0 { t.Fatalf("rollback row count=%d", count) }
}

func TestBeginRejectsNilStore(t *testing.T) {
	if _, err := Begin(context.Background(), nil); err == nil { t.Fatal("expected nil-store error") }
}
