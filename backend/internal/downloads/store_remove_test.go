package downloads

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestDownloadStoreRemoveCancelledResultSemantics(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewDownloadStore(db)

	insert := func(id, state string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO download_jobs(
			id,provider,repo_id,revision,artifact_id,name,quantization,state,
			total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,0,0,0,'',unixepoch(),unixepoch())`,
			id, "huggingface", "acme/demo", "rev", "artifact-"+id, id+".gguf", "", state); err != nil {
			t.Fatal(err)
		}
	}

	insert("cancelled", StateCancelled)
	removed, state, err := store.RemoveCancelled(ctx, "cancelled")
	if err != nil || !removed || state != "" {
		t.Fatalf("cancelled remove: removed=%v state=%q err=%v", removed, state, err)
	}
	if _, err := store.Get(ctx, "cancelled"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cancelled row survived remove: %v", err)
	}

	insert("active", StateQueued)
	removed, state, err = store.RemoveCancelled(ctx, "active")
	if err != nil || removed || state != StateQueued {
		t.Fatalf("active remove: removed=%v state=%q err=%v", removed, state, err)
	}
	if _, err := store.Get(ctx, "active"); err != nil {
		t.Fatalf("active row was removed: %v", err)
	}

	removed, state, err = store.RemoveCancelled(ctx, "missing")
	if removed || state != "" || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing remove: removed=%v state=%q err=%v", removed, state, err)
	}
}
