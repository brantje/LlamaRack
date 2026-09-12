-- +goose Up
ALTER TABLE benchmark_runs ADD COLUMN benchmark_overrides TEXT NOT NULL DEFAULT '{}';
ALTER TABLE benchmark_runs ADD COLUMN effective_benchmark_config TEXT NOT NULL DEFAULT '{}';

UPDATE benchmark_runs
SET effective_benchmark_config = instance_config_snapshot
WHERE effective_benchmark_config = '{}';

-- +goose Down
ALTER TABLE benchmark_runs DROP COLUMN effective_benchmark_config;
ALTER TABLE benchmark_runs DROP COLUMN benchmark_overrides;
