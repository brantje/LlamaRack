package downloads

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/huggingface"
)

func newTestManager(t *testing.T, handler http.Handler) (*Manager, *httptest.Server, context.CancelFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	root := t.TempDir()
	db, err := database.Open(context.Background(), filepath.Join(root, "manager.db"))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hf, err := huggingface.NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := New(ctx, db, filepath.Join(root, "models"), hf)
	t.Cleanup(func() {
		cancel()
		server.Close()
	})
	return manager, server, cancel
}

func waitJob(t *testing.T, manager *Manager, id string, states ...string) Job {
	t.Helper()
	wanted := map[string]bool{}
	for _, state := range states {
		wanted[state] = true
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := manager.Get(context.Background(), id)
		if err == nil && wanted[job.State] {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, err := manager.Get(context.Background(), id)
	t.Fatalf("job did not reach %v: %+v err=%v", states, job, err)
	return Job{}
}

func artifact(repo, revision, id string, files ...huggingface.File) (huggingface.ModelDetail, huggingface.Artifact) {
	var total int64
	for _, file := range files {
		total += file.Size
	}
	return huggingface.ModelDetail{ID: repo, Revision: revision}, huggingface.Artifact{
		ID: id, Name: "demo-Q4_K_M.gguf", Quantization: "Q4_K_M", TotalBytes: total,
		ShardCount: len(files), ExpectedShards: len(files), Complete: true, Files: files,
	}
}

func TestDownloadCompletesSplitArtifactAtomically(t *testing.T) {
	contents := map[string]string{
		"/acme/demo/resolve/rev/demo-Q4_K_M-00001-of-00002.gguf": "abc",
		"/acme/demo/resolve/rev/demo-Q4_K_M-00002-of-00002.gguf": "defg",
	}
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, ok := contents[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", "v1-"+filepath.Base(r.URL.Path))
		w.Header().Set("X-Linked-Size", stringInt64(int64(len(content))))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = io.WriteString(w, content)
	}))
	detail, selected := artifact("acme/demo", "rev", "artifact-1",
		huggingface.File{Path: "demo-Q4_K_M-00001-of-00002.gguf", Size: 3},
		huggingface.File{Path: "demo-Q4_K_M-00002-of-00002.gguf", Size: 4},
	)
	job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, manager, job.ID, StateCompleted)
	if job.DownloadedBytes != 7 || job.TotalBytes != 7 || len(job.Files) != 2 {
		t.Fatalf("completed job = %+v", job)
	}
	for _, file := range job.Files {
		if file.State != StateCompleted || file.LocalPath == "" {
			t.Fatalf("file = %+v", file)
		}
		full := filepath.Join(manager.modelsDir, filepath.FromSlash(file.LocalPath))
		if _, err := os.Stat(full + ".lcm-" + job.ID + ".part"); !os.IsNotExist(err) {
			t.Fatalf("partial file still exists: %v", err)
		}
		if data, err := os.ReadFile(full); err != nil || string(data) != contents["/acme/demo/resolve/rev/"+file.Path] {
			t.Fatalf("final file %s = %q err=%v", full, data, err)
		}
	}
	jobs, err := manager.List(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].State != StateCompleted {
		t.Fatalf("list = %+v err=%v", jobs, err)
	}
}

func TestResumeUsesRangeForMatchingETag(t *testing.T) {
	var mu sync.Mutex
	seenRange := ""
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "6")
		if r.Method == http.MethodHead {
			return
		}
		mu.Lock()
		seenRange = r.Header.Get("Range")
		mu.Unlock()
		if r.Header.Get("Range") == "bytes=3-" {
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "def")
			return
		}
		_, _ = io.WriteString(w, "abcdef")
	}))
	insertJob(t, manager, "resume", StateDownloading, "v1", 3)
	job := Job{ID: "resume", RepoID: "acme/demo"}
	file := File{Path: "demo.gguf", Size: 6}
	finalPath, err := manager.localPath(job, file.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(finalPath+".lcm-resume.part", []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitJob(t, manager, "resume", StateCompleted)
	mu.Lock()
	gotRange := seenRange
	mu.Unlock()
	if gotRange != "bytes=3-" {
		t.Fatalf("range = %q", gotRange)
	}
	if data, err := os.ReadFile(finalPath); err != nil || string(data) != "abcdef" {
		t.Fatalf("resumed file = %q err=%v", data, err)
	}
}

func TestChangedETagRestartsInsteadOfAppending(t *testing.T) {
	var mu sync.Mutex
	seenRange := "sentinel"
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v2")
		w.Header().Set("X-Linked-Size", "6")
		if r.Method == http.MethodHead {
			return
		}
		mu.Lock()
		seenRange = r.Header.Get("Range")
		mu.Unlock()
		_, _ = io.WriteString(w, "uvwxyz")
	}))
	insertJob(t, manager, "changed", StateDownloading, "v1", 3)
	job := Job{ID: "changed", RepoID: "acme/demo"}
	finalPath, _ := manager.localPath(job, "demo.gguf")
	_ = os.MkdirAll(filepath.Dir(finalPath), 0o755)
	if err := os.WriteFile(finalPath+".lcm-changed.part", []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitJob(t, manager, "changed", StateCompleted)
	mu.Lock()
	gotRange := seenRange
	mu.Unlock()
	if gotRange != "" {
		t.Fatalf("changed remote resumed unexpectedly with %q", gotRange)
	}
	if data, err := os.ReadFile(finalPath); err != nil || string(data) != "uvwxyz" {
		t.Fatalf("restarted file = %q err=%v", data, err)
	}
}

func TestRangeUnsupportedRestartsFromZero(t *testing.T) {
	var requests []string
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "6")
		if r.Method == http.MethodHead {
			return
		}
		requests = append(requests, r.Header.Get("Range"))
		_, _ = io.WriteString(w, "abcdef")
	}))
	insertJob(t, manager, "norange", StateDownloading, "v1", 3)
	job := Job{ID: "norange", RepoID: "acme/demo"}
	finalPath, _ := manager.localPath(job, "demo.gguf")
	_ = os.MkdirAll(filepath.Dir(finalPath), 0o755)
	_ = os.WriteFile(finalPath+".lcm-norange.part", []byte("abc"), 0o644)
	if err := manager.ResumePending(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitJob(t, manager, "norange", StateCompleted)
	if len(requests) != 2 || requests[0] != "bytes=3-" || requests[1] != "" {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestCancelAndRetry(t *testing.T) {
	started := make(chan struct{})
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "6")
		if r.Method == http.MethodHead {
			return
		}
		select {
		case <-started:
		default:
			close(started)
		}
		if flusher, ok := w.(http.Flusher); ok {
			_, _ = io.WriteString(w, "abc")
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	detail, selected := artifact("acme/demo", "rev", "cancel-artifact", huggingface.File{Path: "demo.gguf", Size: 6})
	job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("download did not start")
	}
	if err := manager.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	cancelled := waitJob(t, manager, job.ID, StateCancelled)
	if cancelled.State != StateCancelled {
		t.Fatalf("cancelled = %+v", cancelled)
	}
	if err := manager.Cancel(context.Background(), job.ID); err != nil {
		t.Fatalf("idempotent cancel: %v", err)
	}

	manager.hf, _ = huggingface.NewClientWithHTTP("http://example.invalid", nil, &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body := "abcdef"
		if r.Method == http.MethodHead {
			return response(r, http.StatusOK, "", map[string]string{"ETag": "v1", "X-Linked-Size": "6"}), nil
		}
		return response(r, http.StatusOK, body, nil), nil
	})})
	retried, err := manager.Retry(context.Background(), job.ID)
	if err != nil || retried.ID != job.ID {
		t.Fatalf("retry = %+v err=%v", retried, err)
	}
	waitJob(t, manager, job.ID, StateCompleted)
}

func TestCreateValidationDuplicateAndFailures(t *testing.T) {
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	detail := huggingface.ModelDetail{ID: "acme/demo", Revision: "rev"}
	if _, err := manager.CreateHuggingFace(context.Background(), detail, huggingface.Artifact{ID: "x", Complete: false}); err == nil {
		t.Fatal("expected incomplete artifact rejection")
	}
	if _, err := manager.CreateHuggingFace(context.Background(), huggingface.ModelDetail{}, huggingface.Artifact{ID: "x", Complete: true, Files: []huggingface.File{{Path: "x.gguf"}}}); err == nil {
		t.Fatal("expected identity rejection")
	}
	_, unsafe := artifact("acme/demo", "rev", "unsafe", huggingface.File{Path: "../escape.gguf", Size: 1})
	if _, err := manager.CreateHuggingFace(context.Background(), detail, unsafe); err == nil {
		t.Fatal("expected unsafe filename rejection")
	}

	_, err := manager.db.Exec(`INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES('existing','huggingface','acme/demo','rev','same','demo.gguf','',?,1,1,0,'',unixepoch(),unixepoch())`, StateCompleted)
	if err != nil {
		t.Fatal(err)
	}
	_, same := artifact("acme/demo", "rev", "same", huggingface.File{Path: "demo.gguf", Size: 1})
	duplicate, err := manager.CreateHuggingFace(context.Background(), detail, same)
	if err != nil || duplicate.ID != "existing" {
		t.Fatalf("duplicate = %+v err=%v", duplicate, err)
	}
	if _, err := manager.Get(context.Background(), "missing"); err == nil {
		t.Fatal("expected missing get")
	}
	if _, err := manager.Retry(context.Background(), "missing"); err == nil {
		t.Fatal("expected missing retry")
	}
	if err := manager.Cancel(context.Background(), "missing"); err == nil {
		t.Fatal("expected missing cancel")
	}
}

func TestDownloadFailureLeavesPartialUnpromoted(t *testing.T) {
	manager, _, _ := newTestManager(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "6")
		if r.Method == http.MethodHead {
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	detail, selected := artifact("acme/demo", "rev", "fail", huggingface.File{Path: "demo.gguf", Size: 6})
	job, err := manager.CreateHuggingFace(context.Background(), detail, selected)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitJob(t, manager, job.ID, StateFailed)
	if !strings.Contains(failed.Error, "HTTP 502") {
		t.Fatalf("failure = %+v", failed)
	}
	finalPath, _ := manager.localPath(job, "demo.gguf")
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("failed download was promoted: %v", err)
	}
}

func TestPathHelpers(t *testing.T) {
	if safeProviderPath("../x.gguf") || safeProviderPath("x.txt") || safeProviderPath("/x.gguf") || safeProviderPath("a\\x.gguf") || !safeProviderPath("a/x.gguf") {
		t.Fatal("unexpected provider path validation")
	}
	if safeComponent("a/b") != "a_b" || safeComponent("..") != "_" || safeComponent("") != "_" {
		t.Fatal("unexpected safe component")
	}
	manager, _, _ := newTestManager(t, http.NotFoundHandler())
	if _, err := manager.localPath(Job{RepoID: "bad"}, "x.gguf"); err == nil {
		t.Fatal("expected invalid repo local path")
	}
	if _, err := manager.localPath(Job{RepoID: "a/b"}, "../x.gguf"); err == nil {
		t.Fatal("expected unsafe local path")
	}
	if relativeSlash(manager.modelsDir, filepath.Join(manager.modelsDir, "a", "b.gguf")) != "a/b.gguf" {
		t.Fatal("unexpected relative path")
	}
}

func insertJob(t *testing.T, manager *Manager, id, state, etag string, downloaded int64) {
	t.Helper()
	_, err := manager.db.Exec(`INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,6,?,0,'',unixepoch(),unixepoch())`, id, "huggingface", "acme/demo", "rev", "artifact-"+id, "demo.gguf", "", state, downloaded)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.db.Exec(`INSERT INTO download_files(job_id,path,size,oid,state,downloaded_bytes,etag,ordinal,local_path)
VALUES(?,?,6,'',?,?,?,0,'')`, id, "demo.gguf", state, downloaded, etag)
	if err != nil {
		t.Fatal(err)
	}
}

func stringInt64(value int64) string {
	return strings.TrimSpace(strings.ReplaceAll(time.Duration(value).String(), "ns", ""))
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(req *http.Request, status int, body string, headers map[string]string) *http.Response {
	h := make(http.Header)
	for key, value := range headers {
		h.Set(key, value)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: req}
}
