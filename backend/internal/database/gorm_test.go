package database

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestOpenConfiguredUnreachablePostgresDoesNotFallbackOrLeakCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	fallback := filepath.Join(t.TempDir(), "sqlite-fallback", "manager.db")
	password := "p@ss:/?#word"
	dsnURL := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword("llamarack", password),
		Host:   "127.0.0.1:1",
		Path:   "/llamarack",
	}
	query := dsnURL.Query()
	query.Set("sslmode", "disable")
	dsnURL.RawQuery = query.Encode()
	dsn := dsnURL.String()

	store, err := OpenConfigured(ctx, fallback, dsn)
	if store != nil {
		_ = store.Close()
		t.Fatal("unreachable PostgreSQL unexpectedly opened")
	}
	if err == nil {
		t.Fatal("expected explicitly configured PostgreSQL to fail")
	}
	message := err.Error()
	if strings.Contains(message, password) || strings.Contains(message, url.QueryEscape(password)) || strings.Contains(message, dsn) {
		t.Fatalf("database error leaked credentials: %q", message)
	}
	if _, statErr := os.Stat(fallback); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("SQLite fallback was created: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Dir(fallback)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("SQLite fallback directory was created: %v", statErr)
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
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
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


func TestSQLiteClassifiesConstraintErrors(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.ExecContext(ctx, "INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)", "schema_owner", "duplicate", 1)
	if !errors.Is(err, ErrConflict) || !errors.Is(err, ErrIntegrity) {
		t.Fatalf("duplicate classification=%v", err)
	}
	_, err = store.ExecContext(ctx, "INSERT INTO models(id,name,gguf_path,total_bytes,context_length) VALUES(?,?,?,?,?)", "bad", "Bad", "bad.gguf", 1, -1)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("check classification=%v", err)
	}
}


func TestBindPostgresPlaceholdersPreservesQuotedQuestionMarks(t *testing.T) {
	query := `SELECT '?' AS literal, "?" AS identifier, value FROM demo WHERE a=? AND b='it''s ?' AND c=?`
	got := bindPostgresPlaceholders(query)
	want := `SELECT '?' AS literal, "?" AS identifier, value FROM demo WHERE a=$1 AND b='it''s ?' AND c=$2`
	if got != want {
		t.Fatalf("bound query=%q want=%q", got, want)
	}
}

func TestExplicitSQLAdapterAllocationOverhead(t *testing.T) {
	ctx := context.Background()
	open := func(name string, adapter bool) Store {
		t.Helper()
		path := filepath.Join(t.TempDir(), name+".db")
		if adapter {
			store, err := OpenStore(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			return store
		}
		store, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	raw := open("raw", false)
	adapter := open("adapter", true)
	for _, store := range []Store{raw, adapter} {
		if _, err := store.ExecContext(ctx, `INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES(?,?,?)`, "alloc", "value", 1); err != nil {
			t.Fatal(err)
		}
	}
	measureQuery := func(store Store) float64 {
		return testing.AllocsPerRun(100, func() {
			var value string
			if err := store.QueryRowContext(ctx, `SELECT setting_value FROM manager_settings WHERE setting_key=?`, "alloc").Scan(&value); err != nil {
				panic(err)
			}
		})
	}
	measureExec := func(store Store) float64 {
		return testing.AllocsPerRun(100, func() {
			if _, err := store.ExecContext(ctx, `UPDATE manager_settings SET updated_at=updated_at+1 WHERE setting_key=?`, "alloc"); err != nil {
				panic(err)
			}
		})
	}
	rawQuery, adapterQuery := measureQuery(raw), measureQuery(adapter)
	rawExec, adapterExec := measureExec(raw), measureExec(adapter)
	if adapterQuery > rawQuery+4 {
		t.Fatalf("query allocations regressed: raw=%.1f adapter=%.1f", rawQuery, adapterQuery)
	}
	if adapterExec > rawExec+4 {
		t.Fatalf("exec allocations regressed: raw=%.1f adapter=%.1f", rawExec, adapterExec)
	}
}
