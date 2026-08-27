package downloads

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func insertCancelledJob(t *testing.T, manager *Manager, id string) {
	t.Helper()
	_, err := manager.db.ExecContext(context.Background(), `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,0,0,0,'',unixepoch(),unixepoch())`, id, "huggingface", "acme/demo", "rev", "artifact", "demo.gguf", "", StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDetailedListReturnsFileQueryError(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	insertCancelledJob(t, manager, "files-error")
	if _, err := manager.db.ExecContext(context.Background(), `DROP TABLE download_files`); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.detailedList(context.Background()); err == nil {
		t.Fatal("expected file list error")
	}
}

func TestRemoveReturnsDeleteDatabaseError(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	insertCancelledJob(t, manager, "delete-error")
	if _, err := manager.db.ExecContext(context.Background(), `CREATE TRIGGER fail_download_delete BEFORE DELETE ON download_jobs BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), "delete-error"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("remove error = %v", err)
	}
}

func TestRemoveHandlesIgnoredDelete(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	insertCancelledJob(t, manager, "ignored-delete")
	if _, err := manager.db.ExecContext(context.Background(), `CREATE TRIGGER ignore_download_delete BEFORE DELETE ON download_jobs BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), "ignored-delete"); err == nil || !strings.Contains(err.Error(), "only cancelled downloads can be removed") {
		t.Fatalf("remove error = %v", err)
	}
}
