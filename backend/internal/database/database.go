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
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 last_login_at INTEGER
);
CREATE TABLE IF NOT EXISTS sessions (
 id TEXT PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 token_hash TEXT NOT NULL UNIQUE,
 csrf_token_hash TEXT NOT NULL,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 expires_at INTEGER NOT NULL,
 remote_address TEXT NOT NULL DEFAULT '',
 user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions(user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions(expires_at);
CREATE TABLE IF NOT EXISTS api_keys (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 prefix TEXT NOT NULL,
 token_hash TEXT NOT NULL UNIQUE,
 enabled INTEGER NOT NULL DEFAULT 1,
 created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 last_used_at INTEGER,
 revoked_at INTEGER
);
CREATE INDEX IF NOT EXISTS api_keys_token_hash_idx ON api_keys(token_hash);
CREATE TABLE IF NOT EXISTS manager_settings (
 setting_key TEXT PRIMARY KEY,
 setting_value TEXT NOT NULL,
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE IF NOT EXISTS global_options (
 option_key TEXT PRIMARY KEY,
 option_value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS provider_secrets (
 name TEXT PRIMARY KEY,
 ciphertext BLOB NOT NULL,
 nonce BLOB NOT NULL,
 prefix TEXT NOT NULL,
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
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
CREATE TABLE IF NOT EXISTS gguf_index (
 path TEXT PRIMARY KEY,
 size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
 mtime_ns INTEGER NOT NULL,
 gguf_version INTEGER NOT NULL DEFAULT 0,
 tensor_count INTEGER NOT NULL DEFAULT 0 CHECK(tensor_count >= 0),
 metadata_count INTEGER NOT NULL DEFAULT 0 CHECK(metadata_count >= 0),
 architecture TEXT NOT NULL DEFAULT '',
 context_length INTEGER NOT NULL DEFAULT 0,
 block_count INTEGER NOT NULL DEFAULT 0,
 embedding_length INTEGER NOT NULL DEFAULT 0,
 head_count INTEGER NOT NULL DEFAULT 0,
 kv_head_count INTEGER NOT NULL DEFAULT 0,
 key_length INTEGER NOT NULL DEFAULT 0,
 value_length INTEGER NOT NULL DEFAULT 0,
 nextn_predict_layers INTEGER NOT NULL DEFAULT 0,
 has_mtp INTEGER NOT NULL DEFAULT 0,
 mtp_only INTEGER NOT NULL DEFAULT 0,
 projector INTEGER NOT NULL DEFAULT 0,
 inspect_error TEXT NOT NULL DEFAULT '',
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
 request_log_mode TEXT NOT NULL DEFAULT 'metadata',
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
CREATE TABLE IF NOT EXISTS inference_requests (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 started_at INTEGER NOT NULL,
 finished_at INTEGER NOT NULL,
 instance_id TEXT NOT NULL,
 endpoint TEXT NOT NULL,
 api_key_id TEXT,
 api_key_name TEXT,
 api_key_prefix TEXT,
 streaming INTEGER NOT NULL DEFAULT 0,
 status_code INTEGER NOT NULL DEFAULT 0,
 result TEXT NOT NULL,
 duration_ms REAL NOT NULL DEFAULT 0,
 ttft_ms REAL,
 prompt_tokens INTEGER NOT NULL DEFAULT 0,
 generated_tokens INTEGER NOT NULL DEFAULT 0,
 total_tokens INTEGER NOT NULL DEFAULT 0,
 tokens_per_second REAL,
 queue_duration_ms REAL NOT NULL DEFAULT 0,
 load_duration_ms REAL NOT NULL DEFAULT 0,
 autoloaded INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '',
 request_body TEXT,
 response_body TEXT
);
CREATE INDEX IF NOT EXISTS inference_requests_started_at_idx ON inference_requests(started_at DESC);
CREATE INDEX IF NOT EXISTS inference_requests_instance_started_idx ON inference_requests(instance_id,started_at DESC);
CREATE INDEX IF NOT EXISTS inference_requests_api_key_started_idx ON inference_requests(api_key_id,started_at DESC);
CREATE TABLE IF NOT EXISTS observability_counters (
 metric TEXT NOT NULL,
 instance_id TEXT NOT NULL DEFAULT '',
 endpoint TEXT NOT NULL DEFAULT '',
 status_code INTEGER NOT NULL DEFAULT 0,
 result TEXT NOT NULL DEFAULT '',
 streaming INTEGER NOT NULL DEFAULT 0,
 value REAL NOT NULL DEFAULT 0,
 PRIMARY KEY(metric,instance_id,endpoint,status_code,result,streaming)
);
CREATE TABLE IF NOT EXISTS download_jobs (
 id TEXT PRIMARY KEY,
 provider TEXT NOT NULL,
 repo_id TEXT NOT NULL,
 revision TEXT NOT NULL,
 artifact_id TEXT NOT NULL,
 name TEXT NOT NULL,
 quantization TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 total_bytes INTEGER NOT NULL DEFAULT 0 CHECK(total_bytes >= 0),
 downloaded_bytes INTEGER NOT NULL DEFAULT 0 CHECK(downloaded_bytes >= 0),
 speed_bps INTEGER NOT NULL DEFAULT 0 CHECK(speed_bps >= 0),
 error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS download_jobs_state_idx ON download_jobs(state);
CREATE INDEX IF NOT EXISTS download_jobs_identity_idx ON download_jobs(provider,repo_id,revision,artifact_id);
CREATE TABLE IF NOT EXISTS download_files (
 job_id TEXT NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
 path TEXT NOT NULL,
 size INTEGER NOT NULL DEFAULT 0 CHECK(size >= 0),
 oid TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 downloaded_bytes INTEGER NOT NULL DEFAULT 0 CHECK(downloaded_bytes >= 0),
 etag TEXT NOT NULL DEFAULT '',
 ordinal INTEGER NOT NULL DEFAULT 0,
 local_path TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(job_id,path)
);
CREATE TABLE IF NOT EXISTS provider_imports (
 id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
 model_id TEXT REFERENCES models(id) ON DELETE SET NULL,
 instance_id TEXT REFERENCES instances(id) ON DELETE SET NULL,
 owns_model INTEGER NOT NULL DEFAULT 0,
 start_when_ready INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'DOWNLOADING',
 error TEXT NOT NULL DEFAULT '',
 start_attempted INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS provider_imports_job_idx ON provider_imports(job_id);
CREATE INDEX IF NOT EXISTS provider_imports_instance_idx ON provider_imports(instance_id);
`
	_, err := db.ExecContext(ctx, schema)
	return err
}
