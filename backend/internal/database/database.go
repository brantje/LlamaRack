package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			db.Close()
			return nil, fmt.Errorf("sqlite pragma: %w", err)
		}
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 username TEXT NOT NULL UNIQUE,
 password_hash TEXT NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('admin','operator','readonly')) DEFAULT 'admin',
 enabled INTEGER NOT NULL DEFAULT 1,
 created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE IF NOT EXISTS sessions (
 id TEXT PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 token_hash TEXT NOT NULL UNIQUE,
 expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS api_keys (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 prefix TEXT NOT NULL,
 token_hash TEXT NOT NULL UNIQUE,
 enabled INTEGER NOT NULL DEFAULT 1,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 last_used_at INTEGER
);
CREATE TABLE IF NOT EXISTS artifacts (
 id TEXT PRIMARY KEY,
 display_name TEXT NOT NULL,
 local_path TEXT NOT NULL UNIQUE,
 total_bytes INTEGER NOT NULL,
 quantization TEXT,
 created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE IF NOT EXISTS models (
 id TEXT PRIMARY KEY,
 public_id TEXT NOT NULL UNIQUE,
 display_name TEXT,
 artifact_id TEXT NOT NULL REFERENCES artifacts(id),
 enabled INTEGER NOT NULL DEFAULT 1,
 autoload_enabled INTEGER NOT NULL DEFAULT 1,
 always_on INTEGER NOT NULL DEFAULT 0,
 priority TEXT NOT NULL DEFAULT 'normal',
 routing_policy TEXT NOT NULL DEFAULT 'least_active',
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE IF NOT EXISTS model_options (
 model_id TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
 option_key TEXT NOT NULL,
 option_value TEXT NOT NULL,
 PRIMARY KEY(model_id, option_key)
);
CREATE TABLE IF NOT EXISTS instances (
 id TEXT PRIMARY KEY,
 model_id TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
 name TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1,
 preferred INTEGER NOT NULL DEFAULT 0,
 gpu_mode TEXT NOT NULL DEFAULT 'auto',
 gpu_devices TEXT,
 tensor_split TEXT,
 created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return err
	}

	// Early development databases used `autoload`; the model service and API use
	// `autoload_enabled`. Keep upgrades safe by adding the canonical column and
	// copying the legacy value once when needed.
	hasAutoloadEnabled, err := columnExists(ctx, db, "models", "autoload_enabled")
	if err != nil {
		return err
	}
	if !hasAutoloadEnabled {
		if _, err := db.ExecContext(ctx, "ALTER TABLE models ADD COLUMN autoload_enabled INTEGER NOT NULL DEFAULT 1"); err != nil {
			return fmt.Errorf("add models.autoload_enabled: %w", err)
		}
		hasLegacyAutoload, err := columnExists(ctx, db, "models", "autoload")
		if err != nil {
			return err
		}
		if hasLegacyAutoload {
			if _, err := db.ExecContext(ctx, "UPDATE models SET autoload_enabled=autoload"); err != nil {
				return fmt.Errorf("migrate models.autoload: %w", err)
			}
		}
	}
	return nil
}

func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
