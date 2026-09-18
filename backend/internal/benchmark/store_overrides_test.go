package benchmark

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestSQLStorePersistsBenchmarkOverrideLayers(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewSQLStore(db)
	run := testRun(time.Now().UTC())
	value := 1024
	run.BenchmarkOverrides = RuntimeOverrides{BatchSize: &value}
	run.EffectiveConfig = cloneConfigSnapshot(run.InstanceConfig)
	run.EffectiveConfig.Options["batch-size"] = "1024"
	run.EffectiveConfig.Sources["batch-size"] = "benchmark"
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BenchmarkOverrides.BatchSize == nil || *got.BenchmarkOverrides.BatchSize != 1024 {
		t.Fatalf("overrides=%+v", got.BenchmarkOverrides)
	}
	if got.InstanceConfig.Options["batch-size"] != "" || got.EffectiveConfig.Options["batch-size"] != "1024" {
		t.Fatalf("instance=%+v effective=%+v", got.InstanceConfig, got.EffectiveConfig)
	}
}

func TestSQLStoreReadsMigratedLegacyBenchmarkConfig(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Simulate a row created by the original PR schema after migration: no
	// override fields supplied. The migration/defaults must make it readable and
	// preserve the old meaning that effective config equalled the Instance snapshot.
	_, err = db.ExecContext(ctx, `INSERT INTO benchmark_runs(
		id,instance_id,instance_slug_snapshot,instance_name_snapshot,instance_config_snapshot,
		model_id,model_slug_snapshot,model_name_snapshot,artifact_snapshot,workload_profile,
		resolved_argv,mapping_differences,status,created_at,llamarack_version,benchmark_schema_version,parser_schema_version,hardware_snapshot,diagnostic_output
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"legacy", "i", "i", "I", `{"schema_version":1,"gpu_mode":"auto","options":{"threads":"4"}}`,
		"m", "m", "M", `{"path":"model.gguf","fingerprint":"x","size":1,"shard_count":1,"expected_shards":1,"files":[]}`,
		`{"id":"standard-v1","version":2,"prompt_tokens":[512],"generation_tokens":[128],"repetitions":5,"warmup":true}`,
		`["/app/llama-bench"]`, `[]`, "COMPLETED", time.Now().UTC().Unix(), "test", 1, 1,
		`{"observed":{"ram_total_bytes":1,"ram_available_bytes":1,"gpus":[],"processes":[],"collected_at":"0001-01-01T00:00:00Z"},"cpu":{"logical_threads":4,"architecture":"amd64","os":"linux"}}`, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewSQLStore(db).GetRun(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if got.EffectiveConfig.Options["threads"] != "4" || len(RuntimeOverrideKeys(got.BenchmarkOverrides)) != 0 {
		t.Fatalf("legacy run=%+v", got)
	}
}
