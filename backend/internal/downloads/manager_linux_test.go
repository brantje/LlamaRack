//go:build linux

package downloads

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiskFullWriteFailureLeavesDownloadRecoverable(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full is unavailable")
	}
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "disk-full-v1")
		w.Header().Set("X-Linked-Size", "6")
		if r.Method == http.MethodHead {
			return
		}
		_, _ = io.WriteString(w, "abcdef")
	}))
	insertJob(t, manager, "disk-full", StateQueued, "", 0)

	job := Job{ID: "disk-full", RepoID: "acme/demo"}
	finalPath, err := manager.localPath(job, "demo.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	partPath := finalPath + ".lcm-disk-full.part"
	if err := os.Symlink("/dev/full", partPath); err != nil {
		t.Fatal(err)
	}

	if err := manager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := waitJob(t, manager, "disk-full", StateFailed)
	if !strings.Contains(strings.ToLower(failed.Error), "space") {
		t.Fatalf("disk-full error not surfaced: %+v", failed)
	}
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("failed write promoted a final artifact: %v", err)
	}
	if len(failed.Files) != 1 || failed.Files[0].State != StateFailed {
		t.Fatalf("file state after failed write = %+v", failed.Files)
	}

	if err := os.Remove(partPath); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Retry(context.Background(), "disk-full"); err != nil {
		t.Fatal(err)
	}
	completed := waitJob(t, manager, "disk-full", StateCompleted)
	if completed.DownloadedBytes != 6 {
		t.Fatalf("retry did not complete: %+v", completed)
	}
	if data, err := os.ReadFile(finalPath); err != nil || string(data) != "abcdef" {
		t.Fatalf("retried final artifact=%q err=%v", data, err)
	}
}
