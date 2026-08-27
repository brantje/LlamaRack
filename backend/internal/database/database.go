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
	if err := initializeSchema(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func initializeSchema(ctx context.Context, db *sql.DB) error {
	const schema = `
CREATE TABLE IF NOT EXISTS users (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 username TEXT NOT NULL UNIQUE,
 password_hash TEXT NOT NULL,
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
CREATE TABLE IF NOT EXISTS global_options (
 option_key TEXT PRIMARY KEY,
 option_value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS models (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 gguf_path TEXT NOT NULL UNIQUE,
 total_bytes INTEGER NOT NULL,
 quantization TEXT,
 context_length INTEGER NOT NULL DEFAULT 0 CHECK(context_length >= 0),
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
 autoload_enabled INTEGER NOT NULL DEFAULT 1,
 always_on INTEGER NOT NULL DEFAULT 0,
 priority TEXT NOT NULL DEFAULT 'normal',
 eviction_enabled INTEGER NOT NULL DEFAULT 1,
 idle_unload_seconds INTEGER NOT NULL DEFAULT 0 CHECK(idle_unload_seconds >= 0),
 gpu_mode TEXT NOT NULL DEFAULT 'auto',
 gpu_devices TEXT,
 tensor_split TEXT,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS instances_model_id_idx ON instances(model_id);
CREATE TABLE IF NOT EXISTS instance_options (
 instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE ON UPDATE CASCADE,
 option_key TEXT NOT NULL,
 option_value TEXT NOT NULL,
 PRIMARY KEY(instance_id, option_key)
);
`
	_, err := db.ExecContext(ctx, schema)
	return err
}
