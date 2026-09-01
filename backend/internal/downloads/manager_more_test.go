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

func TestExistingCompleteFileIsAdopted(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	detail, selected := artifact("acme/demo", "rev", "existing-file", huggingface.File{Path: "demo.gguf", Size: 6})
	job := Job{ID: "placeholder", RepoID: detail.ID}
	finalPath, err := manager.localPath(job, "demo.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(finalPath, []byte("abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := manager.CreateHuggingFace(context.Background(), detail, selected)
	if err != nil {
		t.Fatal(err)
	}
	completed := waitJob(t, manager, created.ID, StateCompleted)
	if len(completed.Files) != 1 || completed.Files[0].LocalPath == "" || completed.Files[0].DownloadedBytes != 6 {
		t.Fatalf("adopted file = %+v", completed.Files)
	}
}

func TestRemoteSizeChangeFailsWithoutPromotion(t *testing.T) {
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v2")
		w.Header().Set("X-Linked-Size", "7")
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "1234567")
		}
	}))
	detail, selected := artifact("acme/demo", "rev", "size-change", huggingface.File{Path: "demo.gguf", Size: 6})
	job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitJob(t, manager, job.ID, StateFailed)
	if !strings.Contains(failed.Error, "remote size changed") {
		t.Fatalf("error = %q", failed.Error)
	}
	finalPath, _ := manager.localPath(job, "demo.gguf")
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("unexpected final file: %v", err)
	}
}

func TestHeadFailureAndShortBodyFail(t *testing.T) {
	t.Run("head status", func(t *testing.T) {
		manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		detail, selected := artifact("acme/demo", "rev", "head-fail", huggingface.File{Path: "demo.gguf", Size: 6})
		job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
		if err != nil {
			t.Fatal(err)
		}
		failed := waitJob(t, manager, job.ID, StateFailed)
		if !strings.Contains(failed.Error, "metadata request returned HTTP 403") {
			t.Fatalf("error = %q", failed.Error)
		}
	})

	t.Run("short body", func(t *testing.T) {
		manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", "v1")
			w.Header().Set("X-Linked-Size", "6")
			if r.Method != http.MethodHead {
				_, _ = io.WriteString(w, "abc")
			}
		}))
		detail, selected := artifact("acme/demo", "rev", "short", huggingface.File{Path: "demo.gguf", Size: 6})
		job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
		if err != nil {
			t.Fatal(err)
		}
		failed := waitJob(t, manager, job.ID, StateFailed)
		if !strings.Contains(failed.Error, "expected 6 bytes, received 3") {
			t.Fatalf("error = %q", failed.Error)
		}
	})
}

func TestOversizedOrUnverifiablePartialRestarts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		savedETag  string
		partial    string
		remoteETag string
	}{
		{name: "missing saved etag", savedETag: "", partial: "abc", remoteETag: "v1"},
		{name: "oversized partial", savedETag: "v1", partial: "abcdefghi", remoteETag: "v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seenRange := "unset"
			manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", tc.remoteETag)
				w.Header().Set("X-Linked-Size", "6")
				if r.Method == http.MethodHead {
					return
				}
				seenRange = r.Header.Get("Range")
				_, _ = io.WriteString(w, "abcdef")
			}))
			insertJob(t, manager, "restart", StateDownloading, tc.savedETag, int64(len(tc.partial)))
			job := Job{ID: "restart", RepoID: "acme/demo"}
			finalPath, _ := manager.localPath(job, "demo.gguf")
			_ = os.MkdirAll(filepath.Dir(finalPath), 0o755)
			if err := os.WriteFile(finalPath+".lcm-restart.part", []byte(tc.partial), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := manager.ResumePending(context.Background()); err != nil {
				t.Fatal(err)
			}
			waitJob(t, manager, "restart", StateCompleted)
			if seenRange != "" {
				t.Fatalf("unexpected range = %q", seenRange)
			}
		})
	}
}

func TestRunRejectsUnsupportedProviderAndInvalidStoredIdentity(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	_, err := manager.db.Exec(`INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES('other','direct-url','x/y','rev','a','x.gguf','',?,0,0,0,'',unixepoch(),unixepoch())`, StateQueued)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.run(context.Background(), "other"); err == nil || !strings.Contains(err.Error(), "unsupported download provider") {
		t.Fatalf("run error = %v", err)
	}

	_, err = manager.db.Exec(`INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES('badrepo','huggingface','bad','rev','a','x.gguf','',?,1,0,0,'',unixepoch(),unixepoch())`, StateQueued)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.db.Exec(`INSERT INTO download_files(job_id,path,size,oid,state,downloaded_bytes,etag,ordinal,local_path) VALUES('badrepo','x.gguf',1,'',?,0,'',0,'')`, StateQueued)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.run(context.Background(), "badrepo"); err == nil || !strings.Contains(err.Error(), "invalid repository id") {
		t.Fatalf("bad repo run error = %v", err)
	}
}

func TestCompletedFileValidation(t *testing.T) {
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	job := Job{ID: "validate", RepoID: "acme/demo"}
	file := File{Path: "demo.gguf", Size: 3}
	if manager.completedFileValid(job, file) {
		t.Fatal("missing file reported valid")
	}
	path, _ := manager.localPath(job, file.Path)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte("xx"), 0o644)
	if manager.completedFileValid(job, file) {
		t.Fatal("wrong-size file reported valid")
	}
	_ = os.WriteFile(path, []byte("abc"), 0o644)
	if !manager.completedFileValid(job, file) {
		t.Fatal("complete file reported invalid")
	}
}
