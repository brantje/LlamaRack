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

func TestKnownSizeAboveLimitIsRejectedBeforeCreate(t *testing.T) {
	manager, _, _ := newTestManagerLimit(t, http.NotFoundHandler(), 5)
	detail, selected := artifact("acme/demo", "rev", "too-big", huggingface.File{Path: "demo.gguf", Size: 6})
	if _, err := manager.CreateHuggingFace(context.Background(), detail, selected); err == nil || !strings.Contains(err.Error(), "max_download_bytes") {
		t.Fatalf("create error = %v", err)
	}
	jobs, err := manager.List(context.Background())
	if err != nil || len(jobs) != 0 {
		t.Fatalf("job was inserted: %+v err=%v", jobs, err)
	}
}

func TestExactLimitSucceedsAndOneByteOverFails(t *testing.T) {
	t.Run("exact", func(t *testing.T) {
		manager, _, _ := newTestManagerLimit(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", "v1")
			w.Header().Set("X-Linked-Size", "6")
			if r.Method != http.MethodHead {
				_, _ = io.WriteString(w, "abcdef")
			}
		}), 6)
		detail, selected := artifact("acme/demo", "rev", "exact", huggingface.File{Path: "demo.gguf", Size: 6})
		job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
		if err != nil {
			t.Fatal(err)
		}
		completed := waitJob(t, manager, job.ID, StateCompleted)
		if completed.DownloadedBytes != 6 {
			t.Fatalf("completed = %+v", completed)
		}
	})

	t.Run("one byte over", func(t *testing.T) {
		manager, _, _ := newTestManagerLimit(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", "v1")
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			_, _ = io.WriteString(w, "abcdefg")
		}), 6)
		detail := huggingface.ModelDetail{ID: "acme/demo", Revision: "rev"}
		selected := huggingface.Artifact{
			ID: "over", Name: "demo.gguf", Quantization: "Q4_K_M", Complete: true,
			ShardCount: 1, ExpectedShards: 1, Files: []huggingface.File{{Path: "demo.gguf", Size: 0}},
		}
		job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
		if err != nil {
			t.Fatal(err)
		}
		failed := waitJob(t, manager, job.ID, StateFailed)
		if !strings.Contains(failed.Error, "max_download_bytes") {
			t.Fatalf("error = %q", failed.Error)
		}
		finalPath, _ := manager.localPath(job, "demo.gguf")
		if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
			t.Fatalf("over-limit download was promoted: %v", err)
		}
	})
}

func TestUnknownSizeAbortsAtConfiguredLimit(t *testing.T) {
	manager, _, _ := newTestManagerLimit(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = io.WriteString(w, strings.Repeat("a", 32))
	}), 8)
	detail := huggingface.ModelDetail{ID: "acme/demo", Revision: "rev"}
	selected := huggingface.Artifact{
		ID: "unknown", Name: "demo.gguf", Quantization: "Q4_K_M", Complete: true,
		ShardCount: 1, ExpectedShards: 1, Files: []huggingface.File{{Path: "demo.gguf", Size: 0}},
	}
	job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitJob(t, manager, job.ID, StateFailed)
	if !strings.Contains(failed.Error, "max_download_bytes") {
		t.Fatalf("error = %q", failed.Error)
	}
}

func TestSplitArtifactsShareOneDownloadLimit(t *testing.T) {
	manager, _, _ := newTestManagerLimit(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "4")
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "abcd")
		}
	}), 6)
	detail, selected := artifact("acme/demo", "rev", "split-limit",
		huggingface.File{Path: "demo-Q4_K_M-00001-of-00002.gguf", Size: 4},
		huggingface.File{Path: "demo-Q4_K_M-00002-of-00002.gguf", Size: 4},
	)
	if _, err := manager.CreateHuggingFace(context.Background(), detail, selected); err == nil || !strings.Contains(err.Error(), "max_download_bytes") {
		t.Fatalf("split create error = %v", err)
	}
}

func TestSplitUnknownSizeSharesOneDownloadLimit(t *testing.T) {
	manager, _, _ := newTestManagerLimit(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = io.WriteString(w, "abcd")
	}), 6)
	detail := huggingface.ModelDetail{ID: "acme/demo", Revision: "rev"}
	selected := huggingface.Artifact{
		ID: "split-unknown", Name: "demo-Q4_K_M.gguf", Quantization: "Q4_K_M", Complete: true,
		ShardCount: 2, ExpectedShards: 2,
		Files: []huggingface.File{
			{Path: "demo-Q4_K_M-00001-of-00002.gguf", Size: 0},
			{Path: "demo-Q4_K_M-00002-of-00002.gguf", Size: 0},
		},
	}
	job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitJob(t, manager, job.ID, StateFailed)
	if !strings.Contains(failed.Error, "max_download_bytes") {
		t.Fatalf("error = %q", failed.Error)
	}
}

func TestResumeBytesCountTowardDownloadLimit(t *testing.T) {
	manager, _, _ := newTestManagerLimit(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("Range") == "bytes=5-" {
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "fgh")
			return
		}
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = io.WriteString(w, "abcdefgh")
	}), 6)
	insertJob(t, manager, "resume-limit", StateDownloading, "v1", 5)
	job := Job{ID: "resume-limit", RepoID: "acme/demo"}
	finalPath, err := manager.localPath(job, "demo.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPartPath(finalPath, job.ID), []byte("abcde"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := waitJob(t, manager, job.ID, StateFailed)
	if !strings.Contains(failed.Error, "max_download_bytes") {
		t.Fatalf("error = %q", failed.Error)
	}
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("over-limit resume was promoted: %v", err)
	}
}
