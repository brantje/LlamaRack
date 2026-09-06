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
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

var migrationFS fs.FS = embeddedMigrations
var goMigrations = []*goose.Migration{resourceIdentityMigration}

// ErrUnsupportedDatabaseSchema is returned when SQLite contains application tables
// that were not created through embedded Goose migrations.
var ErrUnsupportedDatabaseSchema = errors.New("unsupported database schema: recreate the database or restore a Goose-managed backup")

const (
	schemaOwnerSettingKey   = "schema_owner"
	schemaOwnerSettingValue = "llamarack"
)

type dbClass int

const (
	dbClassEmpty dbClass = iota
	dbClassManaged
	dbClassUnsupported
)

func migrate(ctx context.Context, db *sql.DB) (int64, error) {
	class, err := classifyDatabase(ctx, db)
	if err != nil {
		return 0, err
	}
	if class == dbClassUnsupported {
		return 0, ErrUnsupportedDatabaseSchema
	}

	target, err := maxEmbeddedMigrationVersion()
	if err != nil {
		return 0, err
	}
	if class == dbClassManaged {
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
	return goose.NewProvider(goose.DialectSQLite3, db, fsys,
		goose.WithGoMigrations(goMigrations...),
		goose.WithDisableGlobalRegistry(true),
	)
}

func classifyDatabase(ctx context.Context, db *sql.DB) (dbClass, error) {
	hasGoose, err := tableExists(ctx, db, goose.DefaultTablename)
	if err != nil {
		return dbClassUnsupported, err
	}
	if hasGoose {
		owned, err := hasSchemaOwnership(ctx, db)
		if err != nil {
			return dbClassUnsupported, err
		}
		if !owned {
			return dbClassUnsupported, nil
		}
		return dbClassManaged, nil
	}

	tableCount, err := userTableCount(ctx, db)
	if err != nil {
		return dbClassUnsupported, err
	}
	if tableCount == 0 {
		return dbClassEmpty, nil
	}
	return dbClassUnsupported, nil
}

func hasSchemaOwnership(ctx context.Context, db *sql.DB) (bool, error) {
	exists, err := tableExists(ctx, db, "manager_settings")
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	var value string
	err = db.QueryRowContext(ctx, `
SELECT setting_value
FROM manager_settings
WHERE setting_key = ?
`, schemaOwnerSettingKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return value == schemaOwnerSettingValue, nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
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
	for _, migration := range goMigrations {
		if migration != nil && migration.Version > maxVersion {
			maxVersion = migration.Version
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

func withGoMigrations(migrations []*goose.Migration) func() {
	prev := goMigrations
	goMigrations = migrations
	return func() { goMigrations = prev }
}
