package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/postgres/*.sql
var embeddedPostgresMigrations embed.FS

var postgresMigrationFS fs.FS = embeddedPostgresMigrations

const postgresSchemaMarkerValue = "llamarack:schema-owner:v1"

func migratePostgres(ctx context.Context, db *sql.DB) (int64, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return 0, fmt.Errorf("create PostgreSQL migration locker: %w", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("open PostgreSQL migration lock session: %w", err)
	}
	if err := locker.SessionLock(ctx, conn); err != nil {
		_ = conn.Close()
		return 0, fmt.Errorf("acquire PostgreSQL migration lock: %w", err)
	}

	version, migrateErr := migratePostgresLocked(ctx, db)
	unlockErr := locker.SessionUnlock(context.WithoutCancel(ctx), conn)
	closeErr := conn.Close()
	if migrateErr != nil {
		return 0, errors.Join(migrateErr, unlockErr, closeErr)
	}
	if unlockErr != nil || closeErr != nil {
		return 0, errors.Join(unlockErr, closeErr)
	}
	return version, nil
}

func migratePostgresLocked(ctx context.Context, db *sql.DB) (int64, error) {
	fsys, err := fs.Sub(postgresMigrationFS, "migrations/postgres")
	if err != nil {
		return 0, fmt.Errorf("open PostgreSQL migrations: %w", err)
	}
	target, err := maxMigrationVersion(fsys, ".")
	if err != nil {
		return 0, err
	}

	class, err := classifyPostgresDatabase(ctx, db)
	if err != nil {
		return 0, err
	}
	if class == dbClassUnsupported {
		return 0, ErrUnsupportedDatabaseSchema
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
	if class == dbClassEmpty {
		if err := ensurePostgresSchemaMarker(ctx, db); err != nil {
			return 0, err
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
	if version > target {
		return version, fmt.Errorf("database schema version %d is newer than this binary supports (%d)", version, target)
	}
	owned, err := hasPostgresSchemaOwnership(ctx, db)
	if err != nil {
		return 0, err
	}
	if !owned {
		return 0, fmt.Errorf("%w: PostgreSQL migrations completed without LlamaRack ownership marker", ErrUnsupportedDatabaseSchema)
	}
	if err := ensurePostgresSchemaMarker(ctx, db); err != nil {
		return 0, err
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
		if owned {
			return dbClassManaged, nil
		}
	}

	marker, err := postgresSchemaMarker(ctx, db)
	if err != nil {
		return dbClassUnsupported, err
	}
	if marker == postgresSchemaMarkerValue {
		recoverable, err := postgresBootstrapResidue(ctx, db, hasGoose)
		if err != nil {
			return dbClassUnsupported, err
		}
		if recoverable {
			return dbClassEmpty, nil
		}
		return dbClassUnsupported, nil
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

func postgresSchemaMarker(ctx context.Context, db *sql.DB) (string, error) {
	var marker sql.NullString
	err := db.QueryRowContext(ctx, `SELECT obj_description(oid, 'pg_namespace') FROM pg_namespace WHERE nspname=current_schema()`).Scan(&marker)
	if err != nil {
		return "", err
	}
	return marker.String, nil
}

func ensurePostgresSchemaMarker(ctx context.Context, db *sql.DB) error {
	current, err := postgresSchemaMarker(ctx, db)
	if err != nil {
		return err
	}
	if current == postgresSchemaMarkerValue {
		return nil
	}
	if current != "" {
		return fmt.Errorf("%w: active PostgreSQL schema already has an unrelated schema marker", ErrUnsupportedDatabaseSchema)
	}
	var schema string
	if err := db.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	if strings.TrimSpace(schema) == "" {
		return errors.New("active PostgreSQL schema is empty")
	}
	quoted := `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
	if _, err := db.ExecContext(ctx, `COMMENT ON SCHEMA `+quoted+` IS '`+postgresSchemaMarkerValue+`'`); err != nil {
		return fmt.Errorf("mark PostgreSQL schema ownership: %w", err)
	}
	return nil
}

func postgresBootstrapResidue(ctx context.Context, db *sql.DB, hasGoose bool) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema=current_schema() AND table_type='BASE TABLE'`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		count++
		if name != goose.DefaultTablename {
			return false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if count == 0 {
		return true, nil
	}
	if !hasGoose || count != 1 {
		return false, nil
	}
	var applied int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+goose.DefaultTablename+` WHERE is_applied=true AND version_id>0`).Scan(&applied); err != nil {
		return false, err
	}
	return applied == 0, nil
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
