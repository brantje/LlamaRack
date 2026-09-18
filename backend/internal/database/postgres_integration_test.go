package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresFreshMigrationAndReopen(t *testing.T) {
	dsn := postgresIntegrationDSN(t)
	ctx := context.Background()

	store, err := OpenConfigured(ctx, "", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if store.Dialect() != DialectPostgres {
		t.Fatalf("dialect=%q", store.Dialect())
	}
	fsys, err := fs.Sub(postgresMigrationFS, "migrations/postgres")
	if err != nil {
		t.Fatal(err)
	}
	wantVersion, err := maxMigrationVersion(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := store.QueryRowContext(ctx, `SELECT MAX(version_id) FROM `+goose.DefaultTablename+` WHERE is_applied=true`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != wantVersion {
		t.Fatalf("schema version=%d want=%d", version, wantVersion)
	}
	var owner string
	if err := store.QueryRowContext(ctx, `SELECT setting_value FROM manager_settings WHERE setting_key=?`, schemaOwnerSettingKey).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != schemaOwnerSettingValue {
		t.Fatalf("schema owner=%q", owner)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenConfigured(ctx, "", dsn)
	if err != nil {
		t.Fatalf("reopen migrated PostgreSQL database: %v", err)
	}
	defer reopened.Close()
	if reopened.Dialect() != DialectPostgres {
		t.Fatalf("reopened dialect=%q", reopened.Dialect())
	}
}

func TestPostgresRejectsUnknownSchema(t *testing.T) {
	dsn := postgresIntegrationDSN(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE foreign_application_state(id BIGINT PRIMARY KEY)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = OpenConfigured(context.Background(), "", dsn)
	if err == nil || !strings.Contains(err.Error(), ErrUnsupportedDatabaseSchema.Error()) {
		t.Fatalf("unknown schema error=%v", err)
	}
}

func TestPostgresRejectsNewerSchema(t *testing.T) {
	dsn := postgresIntegrationDSN(t)
	ctx := context.Background()
	store, err := OpenConfigured(ctx, "", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecContext(ctx, `INSERT INTO `+goose.DefaultTablename+`(version_id,is_applied) VALUES(?,true)`, int64(999999)); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = OpenConfigured(ctx, "", dsn)
	if err == nil || !strings.Contains(err.Error(), "newer than this binary supports") {
		t.Fatalf("newer schema error=%v", err)
	}
}

func postgresIntegrationDSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv("LLAMARACK_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("LLAMARACK_TEST_POSTGRES_URL is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "llamarack_database_" + hex.EncodeToString(random[:])
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		_ = admin.Close()
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
