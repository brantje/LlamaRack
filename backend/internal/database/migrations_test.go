package database

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

const baselineVersion = resourceIdentityMigrationVersion

func TestFreshDatabaseMigratesToLatestSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fresh.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	version, err := gooseVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if version != baselineVersion {
		t.Fatalf("version=%d want %d", version, baselineVersion)
	}
	for _, table := range []string{"users", "oidc_providers", "playground_lifecycle_events"} {
		if !tableExistsQuick(ctx, db, table) {
			t.Fatalf("missing table %s", table)
		}
	}
}

func TestReopenAtLatestVersionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "idempotent.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var version int64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version_id) FROM goose_db_version WHERE is_applied=1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != baselineVersion {
		t.Fatalf("applied migration version=%d", version)
	}
}

func TestRepeatedOpenAfterSuccessIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "repeat.db")
	for i := 0; i < 3; i++ {
		db, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
}

func TestForeignGooseHistoryRejected(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "foreign-goose.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `
CREATE TABLE goose_db_version (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 version_id INTEGER NOT NULL,
 is_applied INTEGER NOT NULL,
 tstamp TIMESTAMP DEFAULT (datetime('now'))
);
INSERT INTO goose_db_version(version_id, is_applied) VALUES (1, 1);
`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(ctx, path)
	if !errors.Is(err, ErrUnsupportedDatabaseSchema) {
		t.Fatalf("err=%v", err)
	}
}

func TestUnmanagedDatabaseRejected(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "unmanaged.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(ctx, path)
	if !errors.Is(err, ErrUnsupportedDatabaseSchema) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewerSchemaVersionRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "newer.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO goose_db_version(version_id,is_applied) VALUES(999,1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(ctx, path)
	if err == nil || !strings.Contains(err.Error(), "newer than this binary") {
		t.Fatalf("err=%v", err)
	}
}

func TestThirdMigrationUpgradesFromResourceIdentity(t *testing.T) {
	ctx := context.Background()
	baselineSQL, err := fs.ReadFile(embeddedMigrations, "migrations/00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	testFS := fstest.MapFS{
		"migrations/00001_baseline.sql": {Data: baselineSQL},
		"migrations/00003_test_marker.sql": {Data: []byte(`-- +goose Up
CREATE TABLE migration_marker (id INTEGER PRIMARY KEY);
INSERT INTO migration_marker(id) VALUES (3);

-- +goose Down
`)},
	}
	restore := withMigrationFS(testFS)
	defer restore()

	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	version, err := gooseVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("version=%d", version)
	}
	var marker int
	if err := db.QueryRowContext(ctx, `SELECT id FROM migration_marker`).Scan(&marker); err != nil || marker != 3 {
		t.Fatalf("marker=%d err=%v", marker, err)
	}
}

func TestFailingMigrationRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	baselineSQL, err := fs.ReadFile(embeddedMigrations, "migrations/00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	failSQL := []byte(`-- +goose Up
CREATE TABLE migration_fail_probe (id INTEGER PRIMARY KEY);
INSERT INTO migration_fail_probe(id) VALUES (1);
SELECT invalid_function_that_does_not_exist();

-- +goose Down
`)
	testFS := fstest.MapFS{
		"migrations/00001_baseline.sql": {Data: baselineSQL},
		"migrations/00003_fail.sql":     {Data: failSQL},
	}
	restore := withMigrationFS(testFS)
	defer restore()

	path := filepath.Join(t.TempDir(), "rollback.db")
	db, err := Open(ctx, path)
	if err == nil {
		db.Close()
		t.Fatal("expected failing migration error")
	}
	probe := mustOpenSQLite(path)
	if !tableExistsQuick(ctx, probe, "users") {
		probe.Close()
		t.Fatal("baseline tables should exist after failed third migration")
	}
	if tableExistsQuick(ctx, probe, "migration_fail_probe") {
		probe.Close()
		t.Fatal("failed migration table should not persist")
	}
	probe.Close()

	successFS := fstest.MapFS{
		"migrations/00001_baseline.sql": {Data: baselineSQL},
		"migrations/00003_success.sql": {Data: []byte(`-- +goose Up
CREATE TABLE migration_fail_probe (id INTEGER PRIMARY KEY);
INSERT INTO migration_fail_probe(id) VALUES (3);

-- +goose Down
`)},
	}
	restore()
	restore = withMigrationFS(successFS)
	defer restore()

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var marker int
	if err := db.QueryRowContext(ctx, `SELECT id FROM migration_fail_probe`).Scan(&marker); err != nil || marker != 3 {
		t.Fatalf("marker=%d err=%v", marker, err)
	}
}

func TestClassifyDatabaseStates(t *testing.T) {
	ctx := context.Background()

	emptyDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	class, err := classifyDatabase(ctx, emptyDB)
	if err != nil || class != dbClassEmpty {
		t.Fatalf("empty class=%d err=%v", class, err)
	}
	if err := emptyDB.Close(); err != nil {
		t.Fatal(err)
	}

	managedPath := filepath.Join(t.TempDir(), "managed.db")
	managed, err := Open(ctx, managedPath)
	if err != nil {
		t.Fatal(err)
	}
	class, err = classifyDatabase(ctx, managed)
	if err != nil || class != dbClassManaged {
		t.Fatalf("managed class=%d err=%v", class, err)
	}
	if err := managed.Close(); err != nil {
		t.Fatal(err)
	}

	unmanaged, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "unmanaged.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unmanaged.ExecContext(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	class, err = classifyDatabase(ctx, unmanaged)
	if err != nil || class != dbClassUnsupported {
		t.Fatalf("unsupported class=%d err=%v", class, err)
	}
	if err := unmanaged.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAppliedGooseVersionWithoutTable(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "no-goose.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	version, ok, err := appliedGooseVersion(ctx, db)
	if err != nil || ok || version != 0 {
		t.Fatalf("version=%d ok=%v err=%v", version, ok, err)
	}
}

func TestMaxEmbeddedMigrationVersionRequiresSources(t *testing.T) {
	restoreFS := withMigrationFS(fstest.MapFS{})
	defer restoreFS()
	restoreGo := withGoMigrations(nil)
	defer restoreGo()
	if _, err := maxEmbeddedMigrationVersion(); err == nil {
		t.Fatal("expected missing migrations error")
	}
}

func gooseVersion(ctx context.Context, db *sql.DB) (int64, error) {
	provider, err := newMigrationProvider(db)
	if err != nil {
		return 0, err
	}
	return provider.GetDBVersion(ctx)
}

func tableExistsQuick(ctx context.Context, db *sql.DB, name string) bool {
	exists, err := tableExists(ctx, db, name)
	return err == nil && exists
}

func mustOpenSQLite(path string) *sql.DB {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		panic(err)
	}
	return db
}
