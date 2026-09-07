//go:build linux

package downloads

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestDiskFullWriteFailureLeavesDownloadRecoverable(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full is unavailable")
	}
	original := openPartial
	t.Cleanup(func() { openPartial = original })
	openPartial = func(path string, offset int64) (*os.File, error) {
		return os.OpenFile("/dev/full", os.O_WRONLY, 0)
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

	if err := manager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := waitJob(t, manager, "disk-full", StateFailed)
	if !strings.Contains(strings.ToLower(failed.Error), "space") {
		t.Fatalf("disk-full error not surfaced: %+v", failed)
	}
	job := Job{ID: "disk-full", RepoID: "acme/demo"}
	finalPath, err := manager.localPath(job, "demo.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("failed write promoted a final artifact: %v", err)
	}
	if len(failed.Files) != 1 || failed.Files[0].State != StateFailed {
		t.Fatalf("file state after failed write = %+v", failed.Files)
	}

	openPartial = original
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
