package database

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// TestExistingBaselineDatabaseUpgradesDurableState proves that an already-used
// baseline database is migrated on reopen without losing release-critical state.
func TestExistingBaselineDatabaseUpgradesDurableState(t *testing.T) {
	ctx := context.Background()
	// This qualification test injects its own synthetic v2 migration. Keep the
	// repository's real Go migrations out of this isolated baseline/probe test;
	// they are qualified separately by resource_identity_migration_test.go.
	restoreGo := withGoMigrations(nil)
	defer restoreGo()

	baselineSQL, err := fs.ReadFile(embeddedMigrations, "migrations/00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	baselineFS := fstest.MapFS{
		"migrations/00001_baseline.sql": {Data: baselineSQL},
	}
	restoreBaseline := withMigrationFS(baselineFS)
	path := filepath.Join(t.TempDir(), "upgrade-qualification.db")
	db, err := Open(ctx, path)
	if err != nil {
		restoreBaseline()
		t.Fatal(err)
	}

	statements := []string{
		`INSERT INTO users(id,username,password_hash) VALUES(42,'upgrade-user','password-hash')`,
		`INSERT INTO sessions(id,user_id,token_hash,csrf_token_hash,expires_at) VALUES('session-1',42,'session-hash','csrf-hash',4102444800)`,
		`INSERT INTO service_accounts(id,name,created_by_user_id) VALUES('sa-1','Upgrade Service',42)`,
		`INSERT INTO api_keys(id,name,prefix,token_hash,key_type,owner_service_account_id,created_by_user_id) VALUES('key-1','Upgrade Key','sk-upgrade','key-hash','full','sa-1',42)`,
		`INSERT INTO provider_secrets(name,ciphertext,nonce,prefix) VALUES('huggingface',X'0102',X'0304','hf_')`,
		`INSERT INTO models(id,name,gguf_path,total_bytes,quantization,context_length) VALUES('model-1','Upgrade Model','upgrade/model.gguf',1234,'Q4_K_M',8192)`,
		`INSERT INTO model_options(model_id,option_key,option_value) VALUES('model-1','ctx-size','8192')`,
		`INSERT INTO instances(id,model_id,name,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode) VALUES('instance-1','model-1','Upgrade Instance',1,1,'high',1,60,7,'manual','CUDA0','1','full')`,
		`INSERT INTO instance_options(instance_id,option_key,option_value) VALUES('instance-1','flash-attn','true')`,
		`INSERT INTO manager_settings(setting_key,setting_value) VALUES('qualification.setting','durable')`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			restoreBaseline()
			t.Fatalf("seed baseline database: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		restoreBaseline()
		t.Fatal(err)
	}
	restoreBaseline()

	upgradeFS := fstest.MapFS{
		"migrations/00001_baseline.sql": {Data: baselineSQL},
		"migrations/00002_release_probe.sql": {Data: []byte(`-- +goose Up
CREATE TABLE release_upgrade_probe (id INTEGER PRIMARY KEY, note TEXT NOT NULL);
INSERT INTO release_upgrade_probe(id,note) VALUES (2,'applied');

-- +goose Down
DROP TABLE release_upgrade_probe;
`)},
	}
	restoreUpgrade := withMigrationFS(upgradeFS)
	defer restoreUpgrade()

	verify := func(reopen bool) {
		t.Helper()
		db, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		version, err := gooseVersion(ctx, db)
		if err != nil || version != 2 {
			t.Fatalf("reopen=%v migration version=%d err=%v", reopen, version, err)
		}
		var marker string
		if err := db.QueryRowContext(ctx, `SELECT note FROM release_upgrade_probe WHERE id=2`).Scan(&marker); err != nil || marker != "applied" {
			t.Fatalf("reopen=%v migration marker=%q err=%v", reopen, marker, err)
		}

		checks := []struct {
			query string
			want  string
		}{
			{`SELECT username FROM users WHERE id=42`, "upgrade-user"},
			{`SELECT token_hash FROM sessions WHERE id='session-1'`, "session-hash"},
			{`SELECT name FROM service_accounts WHERE id='sa-1'`, "Upgrade Service"},
			{`SELECT key_type FROM api_keys WHERE id='key-1'`, "full"},
			{`SELECT prefix FROM api_keys WHERE id='key-1'`, "sk-upgrade"},
			{`SELECT token_hash FROM api_keys WHERE id='key-1'`, "key-hash"},
			{`SELECT prefix FROM provider_secrets WHERE name='huggingface'`, "hf_"},
			{`SELECT hex(ciphertext) FROM provider_secrets WHERE name='huggingface'`, "0102"},
			{`SELECT hex(nonce) FROM provider_secrets WHERE name='huggingface'`, "0304"},
			{`SELECT gguf_path FROM models WHERE id='model-1'`, "upgrade/model.gguf"},
			{`SELECT option_value FROM model_options WHERE model_id='model-1' AND option_key='ctx-size'`, "8192"},
			{`SELECT name FROM instances WHERE id='instance-1'`, "Upgrade Instance"},
			{`SELECT option_value FROM instance_options WHERE instance_id='instance-1' AND option_key='flash-attn'`, "true"},
			{`SELECT setting_value FROM manager_settings WHERE setting_key='qualification.setting'`, "durable"},
		}
		for _, check := range checks {
			var got string
			if err := db.QueryRowContext(ctx, check.query).Scan(&got); err != nil || got != check.want {
				t.Fatalf("reopen=%v query=%q got=%q want=%q err=%v", reopen, check.query, got, check.want, err)
			}
		}
	}

	verify(false)
	verify(true)
}
