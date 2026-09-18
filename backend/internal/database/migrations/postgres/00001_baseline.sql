-- +goose Up
CREATE TABLE users (
 id BIGSERIAL PRIMARY KEY,
 username TEXT NOT NULL UNIQUE,
 password_hash TEXT NOT NULL,
 enabled BIGINT NOT NULL DEFAULT 1,
 created_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)),
 last_login_at BIGINT
);

CREATE TABLE sessions (
 id TEXT PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 token_hash TEXT NOT NULL UNIQUE,
 csrf_token_hash TEXT NOT NULL,
 created_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)),
 expires_at BIGINT NOT NULL,
 remote_address TEXT NOT NULL DEFAULT '',
 user_agent TEXT NOT NULL DEFAULT ''
);

CREATE INDEX sessions_user_id_idx ON sessions(user_id);
CREATE INDEX sessions_expires_at_idx ON sessions(expires_at);

CREATE TABLE service_accounts (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 enabled BIGINT NOT NULL DEFAULT 1,
 hidden BIGINT NOT NULL DEFAULT 0,
 created_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)),
 created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE manager_settings (
 setting_key TEXT PRIMARY KEY,
 setting_value TEXT NOT NULL,
 updated_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT))
);

CREATE TABLE global_options (
 option_key TEXT PRIMARY KEY,
 option_value TEXT NOT NULL
);

CREATE TABLE provider_secrets (
 name TEXT PRIMARY KEY,
 ciphertext BYTEA NOT NULL,
 nonce BYTEA NOT NULL,
 prefix TEXT NOT NULL,
 updated_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT))
);

CREATE TABLE models (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 gguf_path TEXT NOT NULL UNIQUE,
 total_bytes BIGINT NOT NULL,
 quantization TEXT,
 context_length BIGINT NOT NULL DEFAULT 0 CHECK(context_length >= 0),
 created_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)),
 updated_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT))
);

CREATE TABLE gguf_index (
 path TEXT PRIMARY KEY,
 size_bytes BIGINT NOT NULL CHECK(size_bytes >= 0),
 mtime_ns BIGINT NOT NULL,
 gguf_version BIGINT NOT NULL DEFAULT 0,
 tensor_count BIGINT NOT NULL DEFAULT 0 CHECK(tensor_count >= 0),
 metadata_count BIGINT NOT NULL DEFAULT 0 CHECK(metadata_count >= 0),
 architecture TEXT NOT NULL DEFAULT '',
 context_length BIGINT NOT NULL DEFAULT 0,
 block_count BIGINT NOT NULL DEFAULT 0,
 embedding_length BIGINT NOT NULL DEFAULT 0,
 head_count BIGINT NOT NULL DEFAULT 0,
 kv_head_count BIGINT NOT NULL DEFAULT 0,
 key_length BIGINT NOT NULL DEFAULT 0,
 value_length BIGINT NOT NULL DEFAULT 0,
 nextn_predict_layers BIGINT NOT NULL DEFAULT 0,
 has_mtp BIGINT NOT NULL DEFAULT 0,
 mtp_only BIGINT NOT NULL DEFAULT 0,
 projector BIGINT NOT NULL DEFAULT 0,
 inspect_error TEXT NOT NULL DEFAULT '',
 updated_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT))
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
 enabled BIGINT NOT NULL DEFAULT 1,
 autoload_enabled BIGINT NOT NULL DEFAULT 1,
 always_on BIGINT NOT NULL DEFAULT 0,
 priority TEXT NOT NULL DEFAULT 'normal',
 eviction_enabled BIGINT NOT NULL DEFAULT 1,
 idle_unload_seconds BIGINT NOT NULL DEFAULT 0 CHECK(idle_unload_seconds >= 0),
 max_pending_requests BIGINT NOT NULL DEFAULT 0 CHECK(max_pending_requests >= 0),
 gpu_mode TEXT NOT NULL DEFAULT 'auto',
 gpu_devices TEXT,
 tensor_split TEXT,
 request_log_mode TEXT NOT NULL DEFAULT 'metadata',
 created_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)),
 updated_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT))
);

CREATE INDEX instances_model_id_idx ON instances(model_id);

CREATE TABLE instance_options (
 instance_id TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE ON UPDATE CASCADE,
 option_key TEXT NOT NULL,
 option_value TEXT NOT NULL,
 PRIMARY KEY(instance_id, option_key)
);

CREATE TABLE inference_requests (
 id BIGSERIAL PRIMARY KEY,
 started_at BIGINT NOT NULL,
 finished_at BIGINT NOT NULL,
 instance_id TEXT NOT NULL,
 endpoint TEXT NOT NULL,
 api_key_id TEXT,
 api_key_name TEXT,
 api_key_prefix TEXT,
 streaming BIGINT NOT NULL DEFAULT 0,
 status_code BIGINT NOT NULL DEFAULT 0,
 result TEXT NOT NULL,
 duration_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
 ttft_ms DOUBLE PRECISION,
 prompt_tokens BIGINT NOT NULL DEFAULT 0,
 generated_tokens BIGINT NOT NULL DEFAULT 0,
 total_tokens BIGINT NOT NULL DEFAULT 0,
 tokens_per_second DOUBLE PRECISION,
 queue_duration_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
 load_duration_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
 autoloaded BIGINT NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '',
 request_body TEXT,
 response_body TEXT,
 trace_id TEXT NOT NULL DEFAULT '',
 call_type TEXT NOT NULL DEFAULT '',
 client_ip TEXT NOT NULL DEFAULT '',
 user_agent TEXT NOT NULL DEFAULT '',
 openai_response_id TEXT,
 openai_response_deleted BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX inference_requests_started_at_idx ON inference_requests(started_at DESC);
CREATE INDEX inference_requests_instance_started_idx ON inference_requests(instance_id,started_at DESC);
CREATE INDEX inference_requests_api_key_started_idx ON inference_requests(api_key_id,started_at DESC);
CREATE INDEX inference_requests_trace_started_idx ON inference_requests(trace_id,started_at);
CREATE INDEX inference_requests_endpoint_started_idx ON inference_requests(endpoint,started_at DESC);
CREATE UNIQUE INDEX inference_requests_openai_response_id_uidx ON inference_requests(openai_response_id) WHERE openai_response_id IS NOT NULL;

CREATE TABLE inference_request_correlations (
 request_id TEXT PRIMARY KEY,
 inference_request_id BIGINT NOT NULL UNIQUE REFERENCES inference_requests(id) ON DELETE CASCADE,
 prompt_tokens_per_second DOUBLE PRECISION
);

CREATE TABLE inference_request_log_context (
 request_id TEXT PRIMARY KEY REFERENCES inference_request_correlations(request_id) ON DELETE CASCADE,
 session_id TEXT NOT NULL DEFAULT '',
 model_id TEXT NOT NULL DEFAULT '',
 model_name TEXT NOT NULL DEFAULT ''
);

CREATE INDEX inference_request_log_context_session_idx ON inference_request_log_context(session_id);
CREATE INDEX inference_request_log_context_model_idx ON inference_request_log_context(model_id);

CREATE TABLE observability_counters (
 metric TEXT NOT NULL,
 instance_id TEXT NOT NULL DEFAULT '',
 endpoint TEXT NOT NULL DEFAULT '',
 status_code BIGINT NOT NULL DEFAULT 0,
 result TEXT NOT NULL DEFAULT '',
 streaming BIGINT NOT NULL DEFAULT 0,
 value DOUBLE PRECISION NOT NULL DEFAULT 0,
 PRIMARY KEY(metric,instance_id,endpoint,status_code,result,streaming)
);

CREATE TABLE hardware_metric_samples (
 collected_at BIGINT NOT NULL,
 metric TEXT NOT NULL,
 device_id TEXT NOT NULL DEFAULT '',
 instance_id TEXT NOT NULL DEFAULT '',
 value DOUBLE PRECISION NOT NULL
);

CREATE INDEX hardware_metric_samples_time_idx ON hardware_metric_samples(collected_at DESC);
CREATE INDEX hardware_metric_samples_metric_time_idx ON hardware_metric_samples(metric,collected_at DESC);
CREATE INDEX hardware_metric_samples_device_time_idx ON hardware_metric_samples(device_id,collected_at DESC);

CREATE TABLE download_jobs (
 id TEXT PRIMARY KEY,
 provider TEXT NOT NULL,
 repo_id TEXT NOT NULL,
 revision TEXT NOT NULL,
 artifact_id TEXT NOT NULL,
 name TEXT NOT NULL,
 quantization TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 total_bytes BIGINT NOT NULL DEFAULT 0 CHECK(total_bytes >= 0),
 downloaded_bytes BIGINT NOT NULL DEFAULT 0 CHECK(downloaded_bytes >= 0),
 speed_bps BIGINT NOT NULL DEFAULT 0 CHECK(speed_bps >= 0),
 error TEXT NOT NULL DEFAULT '',
 created_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)),
 updated_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT))
);

CREATE INDEX download_jobs_state_idx ON download_jobs(state);
CREATE INDEX download_jobs_identity_idx ON download_jobs(provider,repo_id,revision,artifact_id);

CREATE TABLE download_files (
 job_id TEXT NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
 path TEXT NOT NULL,
 size BIGINT NOT NULL DEFAULT 0 CHECK(size >= 0),
 oid TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 downloaded_bytes BIGINT NOT NULL DEFAULT 0 CHECK(downloaded_bytes >= 0),
 etag TEXT NOT NULL DEFAULT '',
 ordinal BIGINT NOT NULL DEFAULT 0,
 local_path TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(job_id,path)
);

CREATE TABLE provider_imports (
 id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
 model_id TEXT REFERENCES models(id) ON DELETE SET NULL,
 instance_id TEXT REFERENCES instances(id) ON DELETE SET NULL,
 owns_model BIGINT NOT NULL DEFAULT 0,
 start_when_ready BIGINT NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'DOWNLOADING',
 error TEXT NOT NULL DEFAULT '',
 start_attempted BIGINT NOT NULL DEFAULT 0,
 created_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)),
 updated_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT))
);

CREATE INDEX provider_imports_job_idx ON provider_imports(job_id);
CREATE INDEX provider_imports_instance_idx ON provider_imports(instance_id);

CREATE TABLE worker_runtime (
 instance_id TEXT PRIMARY KEY,
 generation TEXT NOT NULL,
 pid BIGINT NOT NULL,
 start_ticks BIGINT NOT NULL,
 port BIGINT NOT NULL,
 updated_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT))
);

CREATE TABLE api_keys (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 prefix TEXT NOT NULL,
 token_hash TEXT NOT NULL UNIQUE,
 key_type TEXT NOT NULL CHECK(key_type IN ('inference','management','full')),
 owner_user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
 owner_service_account_id TEXT REFERENCES service_accounts(id) ON DELETE CASCADE,
 enabled BIGINT NOT NULL DEFAULT 1,
 expires_on TEXT,
 instance_ids TEXT NOT NULL DEFAULT '[]',
 created_by_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
 created_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)),
 last_used_at BIGINT,
 CHECK (
  (owner_user_id IS NOT NULL AND owner_service_account_id IS NULL)
  OR (owner_user_id IS NULL AND owner_service_account_id IS NOT NULL)
 )
);

CREATE INDEX api_keys_token_hash_idx ON api_keys(token_hash);

CREATE TABLE oidc_providers (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 enabled BIGINT NOT NULL DEFAULT 1,
 issuer TEXT NOT NULL,
 discovery_url TEXT NOT NULL DEFAULT '',
 client_id TEXT NOT NULL,
 scopes TEXT NOT NULL DEFAULT '["openid"]',
 username_claim TEXT NOT NULL DEFAULT 'preferred_username',
 authorization_endpoint TEXT NOT NULL DEFAULT '',
 token_endpoint TEXT NOT NULL DEFAULT '',
 jwks_url TEXT NOT NULL DEFAULT '',
 last_tested_at BIGINT,
 last_test_succeeded BIGINT NOT NULL DEFAULT 0,
 created_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)),
 updated_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT))
);

CREATE TABLE external_identities (
 id TEXT PRIMARY KEY,
 provider_id TEXT NOT NULL REFERENCES oidc_providers(id) ON DELETE CASCADE,
 issuer TEXT NOT NULL,
 subject TEXT NOT NULL,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 created_at BIGINT NOT NULL DEFAULT (CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT)),
 UNIQUE(provider_id,issuer,subject),
 UNIQUE(provider_id,user_id)
);

CREATE INDEX external_identities_user_idx ON external_identities(user_id);

CREATE TABLE playground_lifecycle_events (
 id BIGSERIAL PRIMARY KEY,
 event TEXT NOT NULL,
 instance_id TEXT NOT NULL,
 correlation_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX playground_lifecycle_events_correlation_idx ON playground_lifecycle_events(correlation_id,event,id);

INSERT INTO manager_settings(setting_key, setting_value, updated_at)
VALUES ('schema_owner', 'llamarack', CAST(EXTRACT(EPOCH FROM CURRENT_TIMESTAMP) AS BIGINT));


CREATE OR REPLACE FUNCTION inference_requests_counters_after_insert_fn()
RETURNS trigger AS $
BEGIN
 IF NEW.autoloaded=1 THEN
  INSERT INTO observability_counters(metric,instance_id,value)
  VALUES('autoload_total',NEW.instance_id,1)
  ON CONFLICT(metric,instance_id,endpoint,status_code,result,streaming)
  DO UPDATE SET value=observability_counters.value+1;
  INSERT INTO observability_counters(metric,instance_id,value)
  VALUES('load_duration_ms_total',NEW.instance_id,NEW.load_duration_ms)
  ON CONFLICT(metric,instance_id,endpoint,status_code,result,streaming)
  DO UPDATE SET value=observability_counters.value+EXCLUDED.value;
  IF NEW.result<>'success' THEN
   INSERT INTO observability_counters(metric,instance_id,value)
   VALUES('failed_start_total',NEW.instance_id,1)
   ON CONFLICT(metric,instance_id,endpoint,status_code,result,streaming)
   DO UPDATE SET value=observability_counters.value+1;
  END IF;
 END IF;
 RETURN NEW;
END;
$ LANGUAGE plpgsql;

CREATE TRIGGER inference_requests_counters_after_insert
AFTER INSERT ON inference_requests
FOR EACH ROW EXECUTE FUNCTION inference_requests_counters_after_insert_fn();

-- +goose Down
