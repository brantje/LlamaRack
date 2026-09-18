-- +goose Up
CREATE TABLE benchmark_runs (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL,
    instance_slug_snapshot TEXT NOT NULL,
    instance_name_snapshot TEXT NOT NULL,
    instance_config_snapshot TEXT NOT NULL,
    model_id TEXT NOT NULL,
    model_slug_snapshot TEXT NOT NULL,
    model_name_snapshot TEXT NOT NULL,
    artifact_snapshot TEXT NOT NULL,
    workload_profile TEXT NOT NULL,
    resolved_argv TEXT NOT NULL,
    mapping_differences TEXT NOT NULL DEFAULT '[]',
    status TEXT NOT NULL CHECK (status IN ('QUEUED','RUNNING','COMPLETED','FAILED','CANCELLED')),
    created_at INTEGER NOT NULL,
    started_at INTEGER,
    completed_at INTEGER,
    llamarack_version TEXT NOT NULL,
    llamarack_commit TEXT,
    llama_cpp_release TEXT,
    llama_cpp_build TEXT,
    llama_bench_version TEXT,
    llama_bench_fingerprint TEXT,
    runtime_variant TEXT,
    benchmark_schema_version INTEGER NOT NULL,
    parser_schema_version INTEGER NOT NULL,
    hardware_snapshot TEXT NOT NULL,
    failure TEXT,
    diagnostic_output TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_benchmark_runs_created ON benchmark_runs(created_at DESC, id DESC);
CREATE INDEX idx_benchmark_runs_instance ON benchmark_runs(instance_id, created_at DESC);
CREATE INDEX idx_benchmark_runs_model ON benchmark_runs(model_id, created_at DESC);
CREATE INDEX idx_benchmark_runs_status ON benchmark_runs(status, created_at DESC);

CREATE TABLE benchmark_results (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id TEXT NOT NULL REFERENCES benchmark_runs(id) ON DELETE CASCADE,
    case_index INTEGER NOT NULL,
    case_id TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL,
    generation_tokens INTEGER NOT NULL,
    repetitions INTEGER NOT NULL,
    avg_ns INTEGER,
    stddev_ns INTEGER,
    avg_ts REAL,
    stddev_ts REAL,
    raw_fields TEXT NOT NULL,
    UNIQUE(run_id, case_index)
);

CREATE INDEX idx_benchmark_results_run ON benchmark_results(run_id, case_index);

-- +goose Down
DROP INDEX IF EXISTS idx_benchmark_results_run;
DROP TABLE IF EXISTS benchmark_results;
DROP INDEX IF EXISTS idx_benchmark_runs_status;
DROP INDEX IF EXISTS idx_benchmark_runs_model;
DROP INDEX IF EXISTS idx_benchmark_runs_instance;
DROP INDEX IF EXISTS idx_benchmark_runs_created;
DROP TABLE IF EXISTS benchmark_runs;
