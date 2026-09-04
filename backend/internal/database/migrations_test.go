package database

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

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

func TestPre10FixturePreservesDurableState(t *testing.T) {
	ctx := context.Background()
	path := createPre10FixtureDB(t)

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
		t.Fatalf("version=%d", version)
	}
	for _, table := range []string{"oidc_providers", "external_identities", "playground_lifecycle_events"} {
		if !tableExistsQuick(ctx, db, table) {
			t.Fatalf("bootstrap missing %s", table)
		}
	}

	var username, modelName, settingValue, keyName string
	if err := db.QueryRowContext(ctx, `SELECT username FROM users WHERE id=1`).Scan(&username); err != nil || username != "admin" {
		t.Fatalf("user=%q err=%v", username, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT name FROM models WHERE id='model-1'`).Scan(&modelName); err != nil || modelName != "Demo" {
		t.Fatalf("model=%q err=%v", modelName, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT setting_value FROM manager_settings WHERE setting_key='session_lifetime_seconds'`).Scan(&settingValue); err != nil || settingValue != "86400" {
		t.Fatalf("setting=%q err=%v", settingValue, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT name FROM api_keys WHERE id='key-1'`).Scan(&keyName); err != nil || keyName != "Admin" {
		t.Fatalf("api key=%q err=%v", keyName, err)
	}
	var counter float64
	if err := db.QueryRowContext(ctx, `SELECT value FROM observability_counters WHERE metric='autoload_total' AND instance_id='inst-1'`).Scan(&counter); err != nil || counter != 3 {
		t.Fatalf("counter=%v err=%v", counter, err)
	}
	var sessionID string
	if err := db.QueryRowContext(ctx, `SELECT session_id FROM inference_request_log_context WHERE request_id='req-1'`).Scan(&sessionID); err != nil || sessionID != "session-1" {
		t.Fatalf("session=%q err=%v", sessionID, err)
	}
}

func TestUnknownLegacySchemaRejected(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "unknown.db")
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
	if !errors.Is(err, ErrUnsupportedLegacySchema) {
		t.Fatalf("err=%v", err)
	}
}

func TestUntypedAPIKeysRejected(t *testing.T) {
	ctx := context.Background()
	path := createPre10FixtureDB(t)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `DROP TABLE api_keys`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE api_keys (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		prefix TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(ctx, path)
	if !errors.Is(err, ErrUnsupportedLegacySchema) {
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

func TestSecondMigrationUpgradesFromBaseline(t *testing.T) {
	ctx := context.Background()
	baselineSQL, err := fs.ReadFile(embeddedMigrations, "migrations/00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	testFS := fstest.MapFS{
		"migrations/00001_baseline.sql": {Data: baselineSQL},
		"migrations/00002_test_marker.sql": {Data: []byte(`-- +goose Up
CREATE TABLE migration_marker (id INTEGER PRIMARY KEY);
INSERT INTO migration_marker(id) VALUES (2);

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
	if version != 2 {
		t.Fatalf("version=%d", version)
	}
	var marker int
	if err := db.QueryRowContext(ctx, `SELECT id FROM migration_marker`).Scan(&marker); err != nil || marker != 2 {
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
		"migrations/00002_fail.sql":     {Data: failSQL},
	}
	restore := withMigrationFS(testFS)
	defer restore()

	path := filepath.Join(t.TempDir(), "rollback.db")
	db, err := Open(ctx, path)
	if err == nil {
		db.Close()
		t.Fatal("expected failing migration error")
	}
	if !tableExistsQuick(ctx, mustOpenSQLite(path), "users") {
		t.Fatal("baseline tables should exist after failed second migration")
	}
	if tableExistsQuick(ctx, mustOpenSQLite(path), "migration_fail_probe") {
		t.Fatal("failed migration table should not persist")
	}

	successFS := fstest.MapFS{
		"migrations/00001_baseline.sql": {Data: baselineSQL},
		"migrations/00002_success.sql": {Data: []byte(`-- +goose Up
CREATE TABLE migration_fail_probe (id INTEGER PRIMARY KEY);
INSERT INTO migration_fail_probe(id) VALUES (2);

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
	if err := db.QueryRowContext(ctx, `SELECT id FROM migration_fail_probe`).Scan(&marker); err != nil || marker != 2 {
		t.Fatalf("marker=%d err=%v", marker, err)
	}
}

func TestStampMigrationVersionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := createPre10FixtureDB(t)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := stampMigrationVersion(ctx, raw, baselineVersion); err != nil {
		t.Fatal(err)
	}
	if err := stampMigrationVersion(ctx, raw, baselineVersion); err != nil {
		t.Fatalf("idempotent stamp: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	version, err := gooseVersion(ctx, db)
	if err != nil || version != baselineVersion {
		t.Fatalf("version=%d err=%v", version, err)
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

	legacyPath := createPre10FixtureDB(t)
	legacy, err := sql.Open("sqlite", legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	class, err = classifyDatabase(ctx, legacy)
	if err != nil || class != dbClassLegacy {
		t.Fatalf("legacy class=%d err=%v", class, err)
	}
	if err := legacy.Close(); err != nil {
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

func TestMaxEmbeddedMigrationVersionRequiresFiles(t *testing.T) {
	restore := withMigrationFS(fstest.MapFS{})
	defer restore()
	if _, err := maxEmbeddedMigrationVersion(); err == nil {
		t.Fatal("expected missing migrations error")
	}
}

func createPre10FixtureDB(t *testing.T) string {
	t.Helper()
	fixture, err := os.ReadFile("testdata/pre10_current.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pre10.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range splitSQLStatements(string(fixture)) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("fixture statement failed: %v\n%s", err, stmt)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
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

func splitSQLStatements(sqlText string) []string {
	var stmts []string
	var current strings.Builder
	for _, line := range strings.Split(sqlText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		current.WriteString(line)
		current.WriteByte('\n')
		if strings.HasSuffix(strings.TrimSpace(line), ";") {
			stmts = append(stmts, current.String())
			current.Reset()
		}
	}
	if tail := strings.TrimSpace(current.String()); tail != "" {
		stmts = append(stmts, tail)
	}
	return stmts
}
