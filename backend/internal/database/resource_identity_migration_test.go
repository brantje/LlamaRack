package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"testing"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestResourceIdentityMigrationPreservesPublicIdentityAndReferences(t *testing.T) {
	ctx := context.Background()
	path := baselineDatabasePath(t, ctx)
	db := mustOpenManaged(t, ctx, path)
	seedResourceIdentityFixture(t, ctx, db, false)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = reopenWithResourceIdentityMigration(t, ctx, path)
	defer db.Close()

	var version int64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version_id) FROM goose_db_version WHERE is_applied=1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != resourceIdentityMigrationVersion {
		t.Fatalf("version=%d", version)
	}

	instances := map[string]string{}
	rows, err := db.QueryContext(ctx, `SELECT slug,id FROM instances ORDER BY slug`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var slug, id string
		if err := rows.Scan(&slug, &id); err != nil {
			t.Fatal(err)
		}
		if !uuidPattern.MatchString(id) {
			t.Fatalf("instance %s id=%q is not UUID", slug, id)
		}
		instances[slug] = id
	}
	rows.Close()
	if len(instances) != 2 || instances["primary-coder"] == "" || instances["imported-coder"] == "" {
		t.Fatalf("instances=%v", instances)
	}

	assertSingleString(t, ctx, db, `SELECT instance_id FROM instance_options WHERE option_key='ctx-size'`, instances["primary-coder"])
	assertSingleString(t, ctx, db, `SELECT instance_id FROM provider_imports WHERE id='import-1'`, instances["imported-coder"])
	assertSingleString(t, ctx, db, `SELECT instance_id FROM worker_runtime WHERE generation='gen-1'`, instances["primary-coder"])
	assertSingleString(t, ctx, db, `SELECT instance_id FROM playground_lifecycle_events WHERE correlation_id='corr-1'`, instances["primary-coder"])

	var rawScopes string
	if err := db.QueryRowContext(ctx, `SELECT instance_ids FROM api_keys WHERE id='key-1'`).Scan(&rawScopes); err != nil {
		t.Fatal(err)
	}
	var scopes []string
	if err := json.Unmarshal([]byte(rawScopes), &scopes); err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 2 || scopes[0] != instances["primary-coder"] || !uuidPattern.MatchString(scopes[1]) || scopes[1] == instances["primary-coder"] {
		t.Fatalf("scopes=%v instances=%v", scopes, instances)
	}

	var liveRequestID, liveSlug string
	if err := db.QueryRowContext(ctx, `SELECT instance_id,model_slug FROM inference_requests WHERE endpoint='/v1/chat/completions'`).Scan(&liveRequestID, &liveSlug); err != nil {
		t.Fatal(err)
	}
	if liveRequestID != instances["primary-coder"] || liveSlug != "primary-coder" {
		t.Fatalf("live history id=%q slug=%q", liveRequestID, liveSlug)
	}
	var staleRequestID, staleSlug string
	if err := db.QueryRowContext(ctx, `SELECT instance_id,model_slug FROM inference_requests WHERE endpoint='/v1/embeddings'`).Scan(&staleRequestID, &staleSlug); err != nil {
		t.Fatal(err)
	}
	if !uuidPattern.MatchString(staleRequestID) || staleSlug != "deleted-coder" {
		t.Fatalf("stale history id=%q slug=%q", staleRequestID, staleSlug)
	}

	models := map[string]string{}
	rows, err = db.QueryContext(ctx, `SELECT id,slug FROM models ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			t.Fatal(err)
		}
		models[id] = slug
	}
	rows.Close()
	if models["model-a"] != "qwen-coder" || models["model-b"] != "qwen-coder-2" {
		t.Fatalf("model slugs=%v", models)
	}

	var violations int
	rows, err = db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("foreign key violations=%d", violations)
	}

	const fixtureID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if _, err := db.ExecContext(ctx, `INSERT INTO instances(id,model_id,name) VALUES(?,'model-a','Missing slug')`, fixtureID); err != nil {
		t.Fatalf("direct fixture insert should receive an id-based slug fallback: %v", err)
	}
	assertSingleString(t, ctx, db, `SELECT slug FROM instances WHERE id='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'`, fixtureID)
	if _, err := db.ExecContext(ctx, `UPDATE models SET slug='' WHERE id='model-a'`); err == nil {
		t.Fatal("expected model slug trigger to reject empty slug")
	}
}

func TestResourceIdentityMigrationFailureRollsBackAtomically(t *testing.T) {
	ctx := context.Background()
	path := baselineDatabasePath(t, ctx)
	db := mustOpenManaged(t, ctx, path)
	seedResourceIdentityFixture(t, ctx, db, true)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := upResourceIdentity(ctx, tx); err == nil {
		tx.Rollback()
		t.Fatal("expected malformed api key scope to fail migration")
	}
	if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(ctx, `PRAGMA table_info(instances)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "slug" {
			t.Fatal("slug column survived rolled back migration")
		}
	}
	assertSingleString(t, ctx, db, `SELECT id FROM instances WHERE name='Primary Coder'`, "primary-coder")
	db.Close()
}

func baselineDatabasePath(t *testing.T, ctx context.Context) string {
	t.Helper()
	path := t.TempDir() + "/baseline.db"
	restore := withGoMigrations(nil)
	db, err := Open(ctx, path)
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func reopenWithResourceIdentityMigration(t *testing.T, ctx context.Context, path string) *sql.DB {
	t.Helper()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func mustOpenManaged(t *testing.T, ctx context.Context, path string) *sql.DB {
	t.Helper()
	restore := withGoMigrations(nil)
	db, err := Open(ctx, path)
	restore()
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func seedResourceIdentityFixture(t *testing.T, ctx context.Context, db *sql.DB, malformedScope bool) {
	t.Helper()
	statements := []string{
		`INSERT INTO users(id,username,password_hash) VALUES(1,'admin','hash')`,
		`INSERT INTO models(id,name,gguf_path,total_bytes,context_length,created_at) VALUES('model-a','Qwen Coder','a.gguf',1,0,1)`,
		`INSERT INTO models(id,name,gguf_path,total_bytes,context_length,created_at) VALUES('model-b','Qwen Coder','b.gguf',1,0,2)`,
		`INSERT INTO instances(id,model_id,name) VALUES('primary-coder','model-a','Primary Coder')`,
		`INSERT INTO instances(id,model_id,name) VALUES('imported-coder','model-b','Imported Coder')`,
		`INSERT INTO instance_options(instance_id,option_key,option_value) VALUES('primary-coder','ctx-size','8192')`,
		`INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state) VALUES('job-1','huggingface','acme/model','main','artifact','model.gguf','completed')`,
		`INSERT INTO provider_imports(id,job_id,model_id,instance_id,state) VALUES('import-1','job-1','model-b','imported-coder','READY')`,
		`INSERT INTO worker_runtime(instance_id,generation,pid,start_ticks,port) VALUES('primary-coder','gen-1',100,200,8080)`,
		`INSERT INTO inference_requests(started_at,finished_at,instance_id,endpoint,result) VALUES(1,2,'primary-coder','/v1/chat/completions','success')`,
		`INSERT INTO inference_requests(started_at,finished_at,instance_id,endpoint,result) VALUES(3,4,'deleted-coder','/v1/embeddings','success')`,
		`INSERT INTO observability_counters(metric,instance_id,value) VALUES('request_total','primary-coder',1)`,
		`INSERT INTO hardware_metric_samples(collected_at,metric,device_id,instance_id,value) VALUES(1,'ram_bytes','host','deleted-coder',1)`,
		`INSERT INTO playground_lifecycle_events(event,instance_id,correlation_id) VALUES('eviction','primary-coder','corr-1')`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed %q: %v", statement, err)
		}
	}
	scope := `["primary-coder","deleted-coder"]`
	if malformedScope {
		scope = `{not-json}`
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO api_keys(id,name,prefix,token_hash,key_type,owner_user_id,instance_ids) VALUES('key-1','Scoped','sk-test','hash-1','inference',1,?)`, scope); err != nil {
		t.Fatal(err)
	}
}

func assertSingleString(t *testing.T, ctx context.Context, db *sql.DB, query, want string) {
	t.Helper()
	var got string
	if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("query %q got=%q want=%q", query, got, want)
	}
}
