package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestResponseOwnerMigrationAddsOwnerColumns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "response-owner.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	columns := map[string]bool{}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(inference_requests)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if !columns["owner_kind"] || !columns["owner_id"] {
		t.Fatalf("missing owner columns: %v", columns)
	}
}
