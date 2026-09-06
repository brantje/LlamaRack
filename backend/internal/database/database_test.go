package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hasColumn(t *testing.T, ctx context.Context, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	return false
}

func TestOpenCreatesSchemaAndEnablesForeignKeys(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "nested", "manager.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, table := range []string{
		"users", "sessions", "service_accounts", "api_keys", "models", "model_options",
		"instances", "instance_options", "worker_runtime", "oidc_providers", "external_identities",
		"playground_lifecycle_events", "goose_db_version",
	} {
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

	for _, column := range []string{"name", "gguf_path", "total_bytes", "quantization", "context_length"} {
		if !hasColumn(t, ctx, db, "models", column) {
			t.Fatalf("models.%s missing", column)
		}
	}
	for _, column := range []string{"model_id", "name", "autoload_enabled", "always_on", "priority", "eviction_enabled", "idle_unload_seconds", "max_pending_requests", "gpu_mode", "gpu_devices", "tensor_split"} {
		if !hasColumn(t, ctx, db, "instances", column) {
			t.Fatalf("instances.%s missing", column)
		}
	}
	for _, column := range []string{"instance_id", "generation", "pid", "start_ticks", "port", "updated_at"} {
		if !hasColumn(t, ctx, db, "worker_runtime", column) {
			t.Fatalf("worker_runtime.%s missing", column)
		}
	}
	for _, forbidden := range []string{"public_id", "autoload_enabled", "always_on", "priority", "eviction_enabled", "idle_unload_seconds", "routing_policy"} {
		if hasColumn(t, ctx, db, "models", forbidden) {
			t.Fatalf("models.%s should have moved to Instances", forbidden)
		}
	}

	if _, err := db.ExecContext(ctx, "INSERT INTO models(id,name,gguf_path,total_bytes) VALUES('m1','One','same.gguf',1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO instances(id,model_id,name) VALUES('one','m1','One')"); err != nil {
		t.Fatal(err)
	}
	var evictionEnabled, idleUnloadSeconds int
	if err := db.QueryRowContext(ctx, "SELECT eviction_enabled,idle_unload_seconds FROM instances WHERE id='one'").Scan(&evictionEnabled, &idleUnloadSeconds); err != nil {
		t.Fatal(err)
	}
	if evictionEnabled != 1 || idleUnloadSeconds != 0 {
		t.Fatalf("unexpected instance defaults enabled=%d idle=%d", evictionEnabled, idleUnloadSeconds)
	}
	if _, err := db.ExecContext(ctx, "UPDATE instances SET idle_unload_seconds=-1 WHERE id='one'"); err == nil {
		t.Fatal("expected non-negative idle timeout constraint")
	}
	if _, err := db.ExecContext(ctx, "UPDATE instances SET max_pending_requests=-1 WHERE id='one'"); err == nil {
		t.Fatal("expected non-negative pending request constraint")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO models(id,name,gguf_path,total_bytes) VALUES('m2','Two','same.gguf',1)"); err == nil {
		t.Fatal("expected unique GGUF path constraint")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO instances(id,model_id,name) VALUES('bad','missing','Bad')"); err == nil {
		t.Fatal("expected instance model foreign key violation")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO worker_runtime(instance_id,generation,pid,start_ticks,port) VALUES('deleted-instance','gen',1,2,10000)"); err != nil {
		t.Fatalf("worker_runtime must not require an instances foreign key: %v", err)
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

func TestOpenCreatesPrivateDatabasePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "manager.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertMode(t, filepath.Dir(path), privateDirPerm)
	assertSQLitePrivate(t, path)
}

func TestOpenRepairsPermissiveExistingDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	path := filepath.Join(dir, "manager.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("expected %s before reopen: %v", name, err)
		}
		if err := os.Chmod(name, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertMode(t, dir, privateDirPerm)
	assertSQLitePrivate(t, path)
}

func TestOpenDoesNotBroadenPrivateDatabasePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	path := filepath.Join(dir, "manager.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dir, privateDirPerm)
	assertMode(t, path, privateFilePerm)
	db, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertMode(t, dir, privateDirPerm)
	assertSQLitePrivate(t, path)
}

func assertSQLitePrivate(t *testing.T, path string) {
	t.Helper()
	assertMode(t, path, privateFilePerm)
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		if _, err := os.Stat(sidecar); err != nil {
			t.Fatalf("expected %s: %v", sidecar, err)
		}
		assertMode(t, sidecar, privateFilePerm)
	}
}

func TestOpenRejectsSharedParentDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o1777); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), filepath.Join(dir, "manager.db"))
	if err == nil || !strings.Contains(err.Error(), "shared directory") {
		t.Fatalf("expected shared parent error, got %v", err)
	}
}
