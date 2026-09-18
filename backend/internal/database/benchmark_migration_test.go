package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBenchmarkMigrationCreatesRetainedHistorySchema(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"benchmark_runs", "benchmark_results"} {
		var name string
		if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name); err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO benchmark_runs(
		id,instance_id,instance_slug_snapshot,instance_name_snapshot,instance_config_snapshot,
		model_id,model_slug_snapshot,model_name_snapshot,artifact_snapshot,workload_profile,resolved_argv,
		status,created_at,llamarack_version,benchmark_schema_version,parser_schema_version,hardware_snapshot
	) VALUES('run','deleted-instance','old-instance','Old Instance','{}','deleted-model','old-model','Old Model','{}','{}','[]','COMPLETED',1,'1.1.0',1,1,'{}')`); err != nil {
		t.Fatalf("history must not require live Instance/Model rows: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO benchmark_results(run_id,case_index,case_id,prompt_tokens,generation_tokens,repetitions,raw_fields) VALUES('run',0,'pp',512,0,5,'{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM benchmark_runs WHERE id='run'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM benchmark_results WHERE run_id='run'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("benchmark result cascade count=%d err=%v", count, err)
	}
}
