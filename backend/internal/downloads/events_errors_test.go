package downloads

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestSubscribeReturnsDatabaseError(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	if err := manager.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := manager.Subscribe(context.Background()); err == nil {
		t.Fatal("expected closed database subscribe error")
	}
}

func TestRemoveCancelledInvokesActiveCancelAndSkipsUnsafeProviderPath(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	ctx := context.Background()
	_, err := manager.db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES('cancel-remove','huggingface','acme/demo','rev','artifact','demo.gguf','',?,1,0,0,'',unixepoch(),unixepoch())`, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('cancel-remove','../unsafe.gguf',1,?,0,0,'')`, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	cancelled := false
	manager.mu.Lock()
	manager.cancels["cancel-remove"] = func() { cancelled = true }
	manager.mu.Unlock()
	if err := manager.Remove(ctx, "cancel-remove"); err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("active download cancel function was not invoked")
	}
}

func TestRemoveReturnsPartialCleanupError(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	ctx := context.Background()
	_, err := manager.db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES('cleanup-error','huggingface','acme/demo','rev','artifact','demo.gguf','',?,1,0,0,'',unixepoch(),unixepoch())`, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('cleanup-error','demo.gguf',1,?,0,0,'')`, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	job := Job{ID: "cleanup-error", RepoID: "acme/demo"}
	finalPath, err := manager.localPath(job, "demo.gguf")
	if err != nil {
		t.Fatal(err)
	}
	partialDir := finalPath + ".lcm-cleanup-error.part"
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(ctx, "cleanup-error"); err == nil {
		t.Fatal("expected partial cleanup error")
	}
}
