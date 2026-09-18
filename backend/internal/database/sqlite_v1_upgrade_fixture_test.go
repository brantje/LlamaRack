package database

import (
	"context"
	_ "embed"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

//go:embed testdata/sqlite-v1.0.3-state.sql
var sqliteV103StateFixture string

func TestSQLiteV103ReleasedDatabaseUpgradesToCurrentSchema(t *testing.T) {
	ctx := context.Background()
	releaseFS := fstest.MapFS{}
	for _, name := range []string{
		"00001_baseline.sql",
		"00003_response_owner.sql",
		"00004_download_files_temp_path.sql",
		"00005_inference_request_timings.sql",
	} {
		data, err := fs.ReadFile(embeddedMigrations, "migrations/"+name)
		if err != nil {
			t.Fatal(err)
		}
		releaseFS["migrations/"+name] = &fstest.MapFile{Data: data}
	}

	path := filepath.Join(t.TempDir(), "llamarack-v1.0.3.db")
	restoreFS := withMigrationFS(releaseFS)
	restoreGo := withGoMigrations([]*goose.Migration{resourceIdentityMigration})
	db, err := Open(ctx, path)
	if err != nil {
		restoreGo()
		restoreFS()
		t.Fatal(err)
	}
	if version, err := gooseVersion(ctx, db); err != nil || version != 5 {
		_ = db.Close()
		restoreGo()
		restoreFS()
		t.Fatalf("v1.0.3 fixture schema version=%d err=%v", version, err)
	}
	if _, err := db.ExecContext(ctx, sqliteV103StateFixture); err != nil {
		_ = db.Close()
		restoreGo()
		restoreFS()
		t.Fatalf("load v1.0.3 state fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		restoreGo()
		restoreFS()
		t.Fatal(err)
	}
	restoreGo()
	restoreFS()

	verify := func(reopen bool) {
		t.Helper()
		db, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("reopen=%v upgrade v1.0.3 database: %v", reopen, err)
		}
		defer db.Close()

		version, err := gooseVersion(ctx, db)
		if err != nil || version != 8 {
			t.Fatalf("reopen=%v schema version=%d err=%v", reopen, version, err)
		}
		checks := []struct {
			query string
			want  string
		}{
			{`SELECT username FROM users WHERE id=42`, "release-user"},
			{`SELECT token_hash FROM sessions WHERE id='release-session'`, "release-session-hash"},
			{`SELECT token_hash FROM api_keys WHERE id='release-key'`, "release-key-hash"},
			{`SELECT instance_ids FROM api_keys WHERE id='release-key'`, `["11111111-1111-4111-8111-111111111111"]`},
			{`SELECT setting_value FROM manager_settings WHERE setting_key='release.setting'`, "preserved"},
			{`SELECT hex(ciphertext) FROM provider_secrets WHERE name='huggingface'`, "010203"},
			{`SELECT slug FROM models WHERE id='release-model'`, "release-model"},
			{`SELECT option_value FROM model_options WHERE model_id='release-model' AND option_key='ctx-size'`, "8192"},
			{`SELECT slug FROM instances WHERE id='11111111-1111-4111-8111-111111111111'`, "release-instance"},
			{`SELECT option_value FROM instance_options WHERE instance_id='11111111-1111-4111-8111-111111111111' AND option_key='flash-attn'`, "true"},
			{`SELECT state FROM download_jobs WHERE id='release-download'`, "COMPLETED"},
			{`SELECT local_path FROM download_files WHERE job_id='release-download' AND path='release.gguf'`, "huggingface/acme/release/release.gguf"},
			{`SELECT state FROM provider_imports WHERE id='release-import'`, "COMPLETED"},
			{`SELECT generation FROM worker_runtime WHERE instance_id='11111111-1111-4111-8111-111111111111'`, "release-generation"},
			{`SELECT trace_id FROM inference_requests WHERE id=77`, "release-trace"},
			{`SELECT finish_reason FROM inference_request_timings WHERE request_id='release-request'`, "stop"},
			{`SELECT correlation_id FROM playground_lifecycle_events WHERE instance_id='11111111-1111-4111-8111-111111111111'`, "release-correlation"},
		}
		for _, check := range checks {
			var got string
			if err := db.QueryRowContext(ctx, check.query).Scan(&got); err != nil || got != check.want {
				t.Fatalf("reopen=%v query=%q got=%q want=%q err=%v", reopen, check.query, got, check.want, err)
			}
		}
		var spillover int
		if err := db.QueryRowContext(ctx, `SELECT system_spillover_enabled FROM instances WHERE id='11111111-1111-4111-8111-111111111111'`).Scan(&spillover); err != nil || spillover != 0 {
			t.Fatalf("reopen=%v system_spillover_enabled=%d err=%v", reopen, spillover, err)
		}
		var sample float64
		if err := db.QueryRowContext(ctx, `SELECT value FROM hardware_metric_samples WHERE metric='gpu_vram_used_bytes'`).Scan(&sample); err != nil || sample != 1024 {
			t.Fatalf("reopen=%v hardware sample=%v err=%v", reopen, sample, err)
		}
		var benchmarkTable string
		if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='benchmark_runs'`).Scan(&benchmarkTable); err != nil || benchmarkTable != "benchmark_runs" {
			t.Fatalf("reopen=%v benchmark schema missing: %q err=%v", reopen, benchmarkTable, err)
		}
	}

	verify(false)
	verify(true)
}
