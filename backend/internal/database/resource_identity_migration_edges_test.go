package database

import (
	"context"
	"database/sql"
	"testing"
)

func TestResourceIdentityMigrationModelSlugFallbacksAndNullableValues(t *testing.T) {
	if got := nullableValue(sql.NullString{}); got != nil {
		t.Fatalf("invalid nullable value=%v want nil", got)
	}
	if got := nullableValue(sql.NullString{String: "0,1", Valid: true}); got != "0,1" {
		t.Fatalf("valid nullable value=%v want 0,1", got)
	}

	ctx := context.Background()
	path := baselineDatabasePath(t, ctx)
	db := mustOpenManaged(t, ctx, path)
	for _, statement := range []string{
		`INSERT INTO models(id,name,gguf_path,total_bytes,context_length,created_at) VALUES('model-empty-a','!!!','empty-a.gguf',1,0,1)`,
		`INSERT INTO models(id,name,gguf_path,total_bytes,context_length,created_at) VALUES('model-empty-b','---','empty-b.gguf',1,0,2)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = reopenWithResourceIdentityMigration(t, ctx, path)
	defer db.Close()
	assertSingleString(t, ctx, db, `SELECT slug FROM models WHERE id='model-empty-a'`, "model")
	assertSingleString(t, ctx, db, `SELECT slug FROM models WHERE id='model-empty-b'`, "model-2")
}
