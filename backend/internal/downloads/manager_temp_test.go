package downloads

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/huggingface"
)

func TestPredictablePartSymlinkIsNotFollowed(t *testing.T) {
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "6")
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "abcdef")
		}
	}))
	insertJob(t, manager, "symlink-attack", StateQueued, "", 0)
	job := Job{ID: "symlink-attack", RepoID: "acme/demo"}
	finalPath, err := manager.localPath(job, "demo.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, legacyPartPath(finalPath, job.ID)); err != nil {
		t.Fatal(err)
	}
	if err := manager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	completed := waitJob(t, manager, job.ID, StateCompleted)
	if completed.DownloadedBytes != 6 {
		t.Fatalf("completed = %+v", completed)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "safe" {
		t.Fatalf("symlink target was written: %q err=%v", data, err)
	}
	if data, err := os.ReadFile(finalPath); err != nil || string(data) != "abcdef" {
		t.Fatalf("final artifact=%q err=%v", data, err)
	}
}

func TestResumeRejectsSymlinkOnPersistedTempPath(t *testing.T) {
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "6")
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "abcdef")
		}
	}))
	insertJob(t, manager, "symlink-resume", StateDownloading, "v1", 3)
	job := Job{ID: "symlink-resume", RepoID: "acme/demo"}
	finalPath, err := manager.localPath(job, "demo.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	tempPath := finalPath + ".lcm-secret.part"
	if err := os.WriteFile(tempPath, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel := relativeSlash(manager.modelsDir, tempPath)
	if _, err := manager.db.Exec(`UPDATE download_files SET temp_path=? WHERE job_id=?`, rel, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tempPath); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, tempPath); err != nil {
		t.Fatal(err)
	}
	if err := manager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := waitJob(t, manager, job.ID, StateFailed)
	if !strings.Contains(failed.Error, "regular file") {
		t.Fatalf("error = %q", failed.Error)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "safe" {
		t.Fatalf("symlink target was written: %q err=%v", data, err)
	}
}

func TestResumeRejectsDirectoryOnPersistedTempPath(t *testing.T) {
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "6")
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "abcdef")
		}
	}))
	insertJob(t, manager, "dir-resume", StateDownloading, "v1", 3)
	job := Job{ID: "dir-resume", RepoID: "acme/demo"}
	finalPath, err := manager.localPath(job, "demo.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(finalPath+".lcm-dir.part", 0o755); err != nil {
		t.Fatal(err)
	}
	rel := relativeSlash(manager.modelsDir, finalPath+".lcm-dir.part")
	if _, err := manager.db.Exec(`UPDATE download_files SET temp_path=? WHERE job_id=?`, rel, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := waitJob(t, manager, "dir-resume", StateFailed)
	if !strings.Contains(failed.Error, "regular file") {
		t.Fatalf("error = %q", failed.Error)
	}
}

func TestLegacyRegularPartRemainsResumable(t *testing.T) {
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "6")
		if r.Method == http.MethodHead {
			return
		}
		if r.Header.Get("Range") == "bytes=3-" {
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "def")
			return
		}
		_, _ = io.WriteString(w, "abcdef")
	}))
	insertJob(t, manager, "legacy-resume", StateDownloading, "v1", 3)
	job := Job{ID: "legacy-resume", RepoID: "acme/demo"}
	finalPath, err := manager.localPath(job, "demo.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPartPath(finalPath, job.ID), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	completed := waitJob(t, manager, job.ID, StateCompleted)
	if completed.DownloadedBytes != 6 {
		t.Fatalf("completed = %+v", completed)
	}
	if data, err := os.ReadFile(finalPath); err != nil || string(data) != "abcdef" {
		t.Fatalf("resumed file = %q err=%v", data, err)
	}
}

func TestSuccessfulDownloadClearsPersistedTempPath(t *testing.T) {
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "3")
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "abc")
		}
	}))
	detail, selected := artifact("acme/demo", "rev", "clear-temp", huggingface.File{Path: "demo.gguf", Size: 3})
	job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
	if err != nil {
		t.Fatal(err)
	}
	completed := waitJob(t, manager, job.ID, StateCompleted)
	if completed.Files[0].TempPath != "" {
		t.Fatalf("temp path leaked: %+v", completed.Files[0])
	}
}
