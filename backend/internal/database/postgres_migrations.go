package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/postgres/*.sql
var embeddedPostgresMigrations embed.FS

var postgresMigrationFS fs.FS = embeddedPostgresMigrations

func migratePostgres(ctx context.Context, db *sql.DB) (int64, error) {
	class, err := classifyPostgresDatabase(ctx, db)
	if err != nil {
		return 0, err
	}
	if class == dbClassUnsupported {
		return 0, ErrUnsupportedDatabaseSchema
	}
	fsys, err := fs.Sub(postgresMigrationFS, "migrations/postgres")
	if err != nil {
		return 0, fmt.Errorf("open PostgreSQL migrations: %w", err)
	}
	target, err := maxMigrationVersion(fsys, ".")
	if err != nil {
		return 0, err
	}
	if class == dbClassManaged {
		current, ok, err := appliedPostgresGooseVersion(ctx, db)
		if err != nil {
			return 0, err
		}
		if ok && current > target {
			return current, fmt.Errorf("database schema version %d is newer than this binary supports (%d)", current, target)
		}
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return 0, err
	}
	if _, err := provider.Up(ctx); err != nil {
		return 0, fmt.Errorf("apply PostgreSQL migrations: %w", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read PostgreSQL migration version: %w", err)
	}
	return version, nil
}

func classifyPostgresDatabase(ctx context.Context, db *sql.DB) (dbClass, error) {
	hasGoose, err := postgresTableExists(ctx, db, goose.DefaultTablename)
	if err != nil {
		return dbClassUnsupported, err
	}
	if hasGoose {
		owned, err := hasPostgresSchemaOwnership(ctx, db)
		if err != nil {
			return dbClassUnsupported, err
		}
		if !owned {
			return dbClassUnsupported, nil
		}
		return dbClassManaged, nil
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'`).Scan(&count); err != nil {
		return dbClassUnsupported, err
	}
	if count == 0 {
		return dbClassEmpty, nil
	}
	return dbClassUnsupported, nil
}

func postgresTableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_type='BASE TABLE' AND table_name=$1
	)`, name).Scan(&exists)
	return exists, err
}

func hasPostgresSchemaOwnership(ctx context.Context, db *sql.DB) (bool, error) {
	exists, err := postgresTableExists(ctx, db, "manager_settings")
	if err != nil || !exists {
		return false, err
	}
	var value string
	err = db.QueryRowContext(ctx, `SELECT setting_value FROM manager_settings WHERE setting_key=$1`, schemaOwnerSettingKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return value == schemaOwnerSettingValue, nil
}

func appliedPostgresGooseVersion(ctx context.Context, db *sql.DB) (int64, bool, error) {
	exists, err := postgresTableExists(ctx, db, goose.DefaultTablename)
	if err != nil || !exists {
		return 0, false, err
	}
	var version sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version_id) FROM `+goose.DefaultTablename+` WHERE is_applied=true`).Scan(&version); err != nil {
		return 0, false, err
	}
	return version.Int64, version.Valid, nil
}

func maxMigrationVersion(fsys fs.FS, dir string) (int64, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return 0, err
	}
	var max int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		version, err := goose.NumericComponent(entry.Name())
		if err == nil && version > max {
			max = version
		}
	}
	if max == 0 {
		return 0, errors.New("no embedded migrations found")
	}
	return max, nil
}
