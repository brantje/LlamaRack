package database

import (
	"context"
	"strings"
	"testing"
)

func TestResourceIdentityMigrationHelpersSurfaceSchemaAndScanFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("legacy instance query failure", func(t *testing.T) {
		db := mustOpenManaged(t, ctx, baselineDatabasePath(t, ctx))
		defer db.Close()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `DROP TABLE instances`); err != nil {
			t.Fatal(err)
		}
		if _, err := readLegacyInstances(ctx, tx); err == nil || !strings.Contains(strings.ToLower(err.Error()), "instances") {
			t.Fatalf("readLegacyInstances query error=%v", err)
		}
	})

	t.Run("legacy instance scan failure", func(t *testing.T) {
		db := mustOpenManaged(t, ctx, baselineDatabasePath(t, ctx))
		defer db.Close()
		if _, err := db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,context_length,created_at) VALUES('bad-model','Bad','bad.gguf',1,0,1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO instances(id,model_id,name,enabled) VALUES('bad-instance','bad-model','Bad Instance','not-an-integer')`); err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := readLegacyInstances(ctx, tx); err == nil {
			t.Fatal("expected invalid integer value to fail legacy Instance scan")
		}
	})

	t.Run("api key scope scan failure", func(t *testing.T) {
		db := mustOpenManaged(t, ctx, baselineDatabasePath(t, ctx))
		defer db.Close()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, statement := range []string{
			`ALTER TABLE api_keys RENAME TO api_keys_original`,
			`CREATE TABLE api_keys(id TEXT, instance_ids TEXT)`,
			`INSERT INTO api_keys(id,instance_ids) VALUES('broken',NULL)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := readAPIKeyScopes(ctx, tx); err == nil {
			t.Fatal("expected NULL instance_ids to fail API key scope scan")
		}
	})

	t.Run("legacy reference query failure", func(t *testing.T) {
		db := mustOpenManaged(t, ctx, baselineDatabasePath(t, ctx))
		defer db.Close()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, `DROP TABLE instance_options`); err != nil {
			t.Fatal(err)
		}
		if _, err := collectLegacyInstanceIDs(ctx, tx); err == nil || !strings.Contains(strings.ToLower(err.Error()), "instance_options") {
			t.Fatalf("collectLegacyInstanceIDs query error=%v", err)
		}
	})

	t.Run("model slug backfill write failure", func(t *testing.T) {
		db := mustOpenManaged(t, ctx, baselineDatabasePath(t, ctx))
		defer db.Close()
		if _, err := db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,context_length,created_at) VALUES('model-a','Model A','a.gguf',1,0,1)`); err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := backfillModelSlugs(ctx, tx); err == nil || !strings.Contains(strings.ToLower(err.Error()), "slug") {
			t.Fatalf("backfillModelSlugs write error=%v", err)
		}
	})
}
