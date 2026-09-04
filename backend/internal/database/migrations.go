package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

const baselineVersion = 1

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

var migrationFS fs.FS = embeddedMigrations

// ErrUnsupportedLegacySchema is returned when a pre-goose database cannot be classified safely.
var ErrUnsupportedLegacySchema = errors.New("unsupported legacy database schema")

type dbClass int

const (
	dbClassEmpty dbClass = iota
	dbClassManaged
	dbClassLegacy
	dbClassUnknown
)

var requiredCoreTables = []string{
	"users",
	"sessions",
	"service_accounts",
	"manager_settings",
	"global_options",
	"provider_secrets",
	"models",
	"gguf_index",
	"model_options",
	"instances",
	"instance_options",
	"inference_requests",
	"inference_request_correlations",
	"inference_request_log_context",
	"observability_counters",
	"hardware_metric_samples",
	"download_jobs",
	"download_files",
	"provider_imports",
	"worker_runtime",
	"api_keys",
}

var requiredColumns = map[string]string{
	"api_keys":          "key_type",
	"instances":         "max_pending_requests",
	"service_accounts":  "hidden",
}

func migrate(ctx context.Context, db *sql.DB) (int64, error) {
	class, err := classifyDatabase(ctx, db)
	if err != nil {
		return 0, err
	}
	switch class {
	case dbClassUnknown:
		return 0, ErrUnsupportedLegacySchema
	case dbClassLegacy:
		if err := bootstrapLegacySchema(ctx, db); err != nil {
			return 0, err
		}
	}

	target, err := maxEmbeddedMigrationVersion()
	if err != nil {
		return 0, err
	}
	if class == dbClassManaged || class == dbClassLegacy {
		current, ok, err := appliedGooseVersion(ctx, db)
		if err != nil {
			return 0, err
		}
		if ok && current > target {
			return current, fmt.Errorf("database schema version %d is newer than this binary supports (%d)", current, target)
		}
	}

	provider, err := newMigrationProvider(db)
	if err != nil {
		return 0, err
	}

	if _, err := provider.Up(ctx); err != nil {
		return 0, fmt.Errorf("apply migrations: %w", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read migration version: %w", err)
	}
	slog.Info("database migrations complete", "schema_version", version)
	return version, nil
}

func newMigrationProvider(db *sql.DB) (*goose.Provider, error) {
	fsys, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open migrations: %w", err)
	}
	return goose.NewProvider(goose.DialectSQLite3, db, fsys)
}

func classifyDatabase(ctx context.Context, db *sql.DB) (dbClass, error) {
	hasGoose, err := tableExists(ctx, db, goose.DefaultTablename)
	if err != nil {
		return dbClassUnknown, err
	}
	if hasGoose {
		return dbClassManaged, nil
	}

	tableCount, err := userTableCount(ctx, db)
	if err != nil {
		return dbClassUnknown, err
	}
	if tableCount == 0 {
		return dbClassEmpty, nil
	}

	for _, table := range requiredCoreTables {
		exists, err := tableExists(ctx, db, table)
		if err != nil {
			return dbClassUnknown, err
		}
		if !exists {
			return dbClassUnknown, nil
		}
	}
	for table, column := range requiredColumns {
		ok, err := columnExists(ctx, db, table, column)
		if err != nil {
			return dbClassUnknown, err
		}
		if !ok {
			return dbClassUnknown, nil
		}
	}
	return dbClassLegacy, nil
}

func bootstrapLegacySchema(ctx context.Context, db *sql.DB) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS oidc_providers (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1,
 issuer TEXT NOT NULL,
 discovery_url TEXT NOT NULL DEFAULT '',
 client_id TEXT NOT NULL,
 scopes TEXT NOT NULL DEFAULT '["openid"]',
 username_claim TEXT NOT NULL DEFAULT 'preferred_username',
 authorization_endpoint TEXT NOT NULL DEFAULT '',
 token_endpoint TEXT NOT NULL DEFAULT '',
 jwks_url TEXT NOT NULL DEFAULT '',
 last_tested_at INTEGER,
 last_test_succeeded INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
)`,
		`CREATE TABLE IF NOT EXISTS external_identities (
 id TEXT PRIMARY KEY,
 provider_id TEXT NOT NULL REFERENCES oidc_providers(id) ON DELETE CASCADE,
 issuer TEXT NOT NULL,
 subject TEXT NOT NULL,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 UNIQUE(provider_id,issuer,subject),
 UNIQUE(provider_id,user_id)
)`,
		`CREATE INDEX IF NOT EXISTS external_identities_user_idx ON external_identities(user_id)`,
		`CREATE TABLE IF NOT EXISTS playground_lifecycle_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 event TEXT NOT NULL,
 instance_id TEXT NOT NULL,
 correlation_id TEXT NOT NULL DEFAULT ''
)`,
		`CREATE INDEX IF NOT EXISTS playground_lifecycle_events_correlation_idx ON playground_lifecycle_events(correlation_id,event,id)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("bootstrap legacy schema: %w", err)
		}
	}
	return stampMigrationVersion(ctx, db, baselineVersion)
}

func stampMigrationVersion(ctx context.Context, db *sql.DB, version int64) error {
	store, err := database.NewStore(goose.DialectSQLite3, goose.DefaultTablename)
	if err != nil {
		return err
	}
	exists, err := tableExists(ctx, db, goose.DefaultTablename)
	if err != nil {
		return err
	}
	if !exists {
		if err := store.CreateVersionTable(ctx, db); err != nil {
			return err
		}
	}
	if _, err := store.GetMigration(ctx, db, 0); err != nil {
		if !errors.Is(err, database.ErrVersionNotFound) {
			return err
		}
		if err := store.Insert(ctx, db, database.InsertRequest{Version: 0}); err != nil {
			return err
		}
	}
	if _, err := store.GetMigration(ctx, db, version); err != nil {
		if !errors.Is(err, database.ErrVersionNotFound) {
			return err
		}
		return store.Insert(ctx, db, database.InsertRequest{Version: version})
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return found == name, nil
}

func userTableCount(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM sqlite_master
WHERE type='table'
  AND name NOT LIKE 'sqlite_%'
`).Scan(&count)
	return count, err
}

func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}

func appliedGooseVersion(ctx context.Context, db *sql.DB) (int64, bool, error) {
	exists, err := tableExists(ctx, db, goose.DefaultTablename)
	if err != nil {
		return 0, false, err
	}
	if !exists {
		return 0, false, nil
	}
	var version sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version_id) FROM `+goose.DefaultTablename+` WHERE is_applied=1`).Scan(&version); err != nil {
		return 0, false, err
	}
	if !version.Valid {
		return 0, false, nil
	}
	return version.Int64, true, nil
}

func maxEmbeddedMigrationVersion() (int64, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return 0, err
	}
	var maxVersion int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		version, err := goose.NumericComponent(entry.Name())
		if err != nil {
			continue
		}
		if version > maxVersion {
			maxVersion = version
		}
	}
	if maxVersion == 0 {
		return 0, fmt.Errorf("no embedded migrations found")
	}
	return maxVersion, nil
}

func withMigrationFS(fsys fs.FS) func() {
	prev := migrationFS
	migrationFS = fsys
	return func() { migrationFS = prev }
}
