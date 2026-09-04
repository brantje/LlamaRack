-- Supported pre-1.0 snapshot fixture: core schema without goose_db_version.
-- Optional lazy tables (oidc_providers, external_identities, playground_lifecycle_events) are absent.

CREATE TABLE users (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 username TEXT NOT NULL UNIQUE,
 password_hash TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 last_login_at INTEGER
);

CREATE TABLE sessions (
 id TEXT PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 token_hash TEXT NOT NULL UNIQUE,
 csrf_token_hash TEXT NOT NULL,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 expires_at INTEGER NOT NULL,
 remote_address TEXT NOT NULL DEFAULT '',
 user_agent TEXT NOT NULL DEFAULT ''
);

CREATE TABLE service_accounts (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1,
 hidden INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE manager_settings (
 setting_key TEXT PRIMARY KEY,
 setting_value TEXT NOT NULL,
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE global_options (
 option_key TEXT PRIMARY KEY,
 option_value TEXT NOT NULL
);

CREATE TABLE provider_secrets (
 name TEXT PRIMARY KEY,
 ciphertext BLOB NOT NULL,
 nonce BLOB NOT NULL,
 prefix TEXT NOT NULL,
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE models (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 gguf_path TEXT NOT NULL UNIQUE,
 total_bytes INTEGER NOT NULL,
 quantization TEXT,
 context_length INTEGER NOT NULL DEFAULT 0 CHECK(context_length >= 0),
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE gguf_index (
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

CREATE TABLE model_options (
 model_id TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
 option_key TEXT NOT NULL,
 option_value TEXT NOT NULL,
 PRIMARY KEY(model_id, option_key)
);

CREATE TABLE instances (
 id TEXT PRIMARY KEY,
 model_id TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
 name TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1,
 autoload_enabled INTEGER NOT NULL DEFAULT 1,
 always_on INTEGER NOT NULL DEFAULT 0,
 priority TEXT NOT NULL DEFAULT 'normal',
 eviction_enabled INTEGER NOT NULL DEFAULT 1,
 idle_unload_seconds INTEGER NOT NULL DEFAULT 0 CHECK(idle_unload_seconds >= 0),
 max_pending_requests INTEGER NOT NULL DEFAULT 0 CHECK(max_pending_requests >= 0),
 gpu_mode TEXT NOT NULL DEFAULT 'auto',
 gpu_devices TEXT,
 tensor_split TEXT,
 request_log_mode TEXT NOT NULL DEFAULT 'metadata',
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE instance_options (
 instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE ON UPDATE CASCADE,
 option_key TEXT NOT NULL,
 option_value TEXT NOT NULL,
 PRIMARY KEY(instance_id, option_key)
);

CREATE TABLE inference_requests (
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
 response_body TEXT,
 trace_id TEXT NOT NULL DEFAULT '',
 call_type TEXT NOT NULL DEFAULT '',
 client_ip TEXT NOT NULL DEFAULT '',
 user_agent TEXT NOT NULL DEFAULT '',
 openai_response_id TEXT,
 openai_response_deleted INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE inference_request_correlations (
 request_id TEXT PRIMARY KEY,
 inference_request_id INTEGER NOT NULL UNIQUE REFERENCES inference_requests(id) ON DELETE CASCADE,
 prompt_tokens_per_second REAL
);

CREATE TABLE inference_request_log_context (
 request_id TEXT PRIMARY KEY REFERENCES inference_request_correlations(request_id) ON DELETE CASCADE,
 session_id TEXT NOT NULL DEFAULT '',
 model_id TEXT NOT NULL DEFAULT '',
 model_name TEXT NOT NULL DEFAULT ''
);

CREATE TABLE observability_counters (
 metric TEXT NOT NULL,
 instance_id TEXT NOT NULL DEFAULT '',
 endpoint TEXT NOT NULL DEFAULT '',
 status_code INTEGER NOT NULL DEFAULT 0,
 result TEXT NOT NULL DEFAULT '',
 streaming INTEGER NOT NULL DEFAULT 0,
 value REAL NOT NULL DEFAULT 0,
 PRIMARY KEY(metric,instance_id,endpoint,status_code,result,streaming)
);

CREATE TABLE hardware_metric_samples (
 collected_at INTEGER NOT NULL,
 metric TEXT NOT NULL,
 device_id TEXT NOT NULL DEFAULT '',
 instance_id TEXT NOT NULL DEFAULT '',
 value REAL NOT NULL
);

CREATE TABLE download_jobs (
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

CREATE TABLE download_files (
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

CREATE TABLE provider_imports (
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

CREATE TABLE worker_runtime (
 instance_id TEXT PRIMARY KEY,
 generation TEXT NOT NULL,
 pid INTEGER NOT NULL,
 start_ticks INTEGER NOT NULL,
 port INTEGER NOT NULL,
 updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE api_keys (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 prefix TEXT NOT NULL,
 token_hash TEXT NOT NULL UNIQUE,
 key_type TEXT NOT NULL CHECK(key_type IN ('inference','management','full')),
 owner_user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
 owner_service_account_id TEXT REFERENCES service_accounts(id) ON DELETE CASCADE,
 enabled INTEGER NOT NULL DEFAULT 1,
 expires_on TEXT,
 instance_ids TEXT NOT NULL DEFAULT '[]',
 created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
 created_at INTEGER NOT NULL DEFAULT (unixepoch()),
 last_used_at INTEGER,
 CHECK (
  (owner_user_id IS NOT NULL AND owner_service_account_id IS NULL)
  OR (owner_user_id IS NULL AND owner_service_account_id IS NOT NULL)
 )
);

INSERT INTO users(id,username,password_hash,enabled,created_at) VALUES(1,'admin','hash',1,100);
INSERT INTO sessions(id,user_id,token_hash,csrf_token_hash,expires_at) VALUES('sess-1',1,'token-hash','csrf-hash',9999999999);
INSERT INTO service_accounts(id,name,enabled,hidden,created_at,created_by_user_id) VALUES('sa-1','automation',1,0,100,1);
INSERT INTO api_keys(id,name,prefix,token_hash,key_type,owner_user_id,instance_ids) VALUES('key-1','Admin','sk-abcd','token-hash','full',1,'[]');
INSERT INTO manager_settings(setting_key,setting_value,updated_at) VALUES('session_lifetime_seconds','86400',100);
INSERT INTO provider_secrets(name,ciphertext,nonce,prefix,updated_at) VALUES('hf',X'0102',X'0304','hf_',100);
INSERT INTO global_options(option_key,option_value) VALUES('ctx-size','4096');
INSERT INTO models(id,name,gguf_path,total_bytes,quantization,context_length) VALUES('model-1','Demo','/models/demo.gguf',123,'Q4_K_M',8192);
INSERT INTO model_options(model_id,option_key,option_value) VALUES('model-1','threads','8');
INSERT INTO instances(id,model_id,name,request_log_mode) VALUES('inst-1','model-1','Demo Instance','metadata');
INSERT INTO instance_options(instance_id,option_key,option_value) VALUES('inst-1','n-gpu-layers','35');
INSERT INTO inference_requests(id,started_at,finished_at,instance_id,endpoint,result,duration_ms,trace_id,call_type) VALUES(1,1000,2000,'inst-1','/v1/chat/completions','success',1000,'trace-1','chat');
INSERT INTO inference_request_correlations(request_id,inference_request_id,prompt_tokens_per_second) VALUES('req-1',1,42.5);
INSERT INTO inference_request_log_context(request_id,session_id,model_id,model_name) VALUES('req-1','session-1','model-1','Demo');
INSERT INTO observability_counters(metric,instance_id,value) VALUES('autoload_total','inst-1',3);
