package downloads

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSubscribeEmitsChangesAndDeletion(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	ctx := context.Background()
	_, err := manager.db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES('watch','huggingface','acme/demo','rev','artifact','demo.gguf','Q4_K_M',?,100,0,0,'',unixepoch(),unixepoch())`, StateQueued)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('watch','demo.gguf',100,?,0,0,'')`, StateQueued)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, events, cancel, err := manager.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if len(snapshot) != 1 || snapshot[0].ID != "watch" || len(snapshot[0].Files) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	if _, err := manager.db.ExecContext(ctx, "UPDATE download_jobs SET state=?,downloaded_bytes=25,speed_bps=10 WHERE id='watch'", StateDownloading); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.db.ExecContext(ctx, "UPDATE download_files SET state=?,downloaded_bytes=25 WHERE job_id='watch'", StateDownloading); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-events:
		if event.Type != "download" || event.Job == nil || event.Job.ID != "watch" || event.Job.DownloadedBytes != 25 || len(event.Job.Files) != 1 || event.Job.Files[0].DownloadedBytes != 25 {
			t.Fatalf("progress event = %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for progress event")
	}

	if _, err := manager.db.ExecContext(ctx, "UPDATE download_jobs SET state=? WHERE id='watch'", StateCancelled); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Type == "download" && event.Job != nil && event.Job.State == StateCancelled {
				goto cancelled
			}
		case <-deadline:
			t.Fatal("timed out waiting for cancelled event")
		}
	}

cancelled:
	if err := manager.Remove(ctx, "watch"); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Type != "download_deleted" || event.ID != "watch" {
			t.Fatalf("delete event = %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delete event")
	}
}

func TestRemoveCancelledDownloadCleansPartialButKeepsPromotedFile(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	ctx := context.Background()
	_, err := manager.db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES('remove','huggingface','acme/demo','rev','artifact','demo.gguf','',?,6,3,0,'',unixepoch(),unixepoch())`, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('remove','demo.gguf',6,?,3,0,'')`, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}

	job := Job{ID: "remove", RepoID: "acme/demo"}
	finalPath, err := manager.localPath(job, "demo.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(finalPath, []byte("final"), 0o644); err != nil {
		t.Fatal(err)
	}
	partialPath := finalPath + ".lcm-remove.part"
	if err := os.WriteFile(partialPath, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := manager.Remove(ctx, "remove"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial still exists: %v", err)
	}
	if data, err := os.ReadFile(finalPath); err != nil || string(data) != "final" {
		t.Fatalf("promoted file was removed: %q err=%v", data, err)
	}
	if _, err := manager.Get(ctx, "remove"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("removed job lookup error = %v", err)
	}
	if err := manager.Remove(ctx, "remove"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second remove error = %v", err)
	}
}

func TestRemoveRejectsNonCancelledDownload(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	_, err := manager.db.Exec(`INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES('active','huggingface','acme/demo','rev','artifact','demo.gguf','',?,1,0,0,'',unixepoch(),unixepoch())`, StateQueued)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), "active"); err == nil {
		t.Fatal("expected non-cancelled removal rejection")
	}
}
