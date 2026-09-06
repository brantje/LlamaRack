package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/huggingface"
)

type huggingFaceFixture struct {
	handler http.Handler
	cookie  *http.Cookie
	server  *httptest.Server
}

func newHuggingFaceFixture(t *testing.T) huggingFaceFixture {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" && r.Header.Get("Authorization") != "Bearer hf_test" {
			t.Fatalf("unexpected authorization %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.URL.Path == "/api/models":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "acme/demo", "author": "acme", "downloads": 10, "likes": 2, "tags": []string{"gguf"}}})
		case r.URL.Path == "/api/models/acme/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "acme/demo", "author": "acme", "sha": "rev1", "cardData": map[string]any{"description": "Demo"},
				"siblings": []map[string]any{{"rfilename": "demo-Q4_K_M.gguf", "size": 1, "blobId": "oid1"}},
			})
		case r.URL.Path == "/acme/demo/resolve/rev1/demo-Q4_K_M.gguf":
			w.Header().Set("ETag", "v1")
			w.Header().Set("X-Linked-Size", "1")
			if r.Method != http.MethodHead {
				_, _ = w.Write([]byte("x"))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)

	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "admin", "password1234"); err != nil {
		t.Fatal(err)
	}
	token, _, _, err := authService.LoginWithMetadata(ctx, "admin", "password1234", "127.0.0.1", "huggingface-test")
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := huggingface.NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	hf, err := huggingface.NewClientWithHTTP(provider.URL, secrets.GetToken, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	downloadManager := downloads.New(context.Background(), db, filepath.Join(root, "models"), hf)
	return huggingFaceFixture{
		handler: NewHuggingFaceHandler(authService, hf, secrets, downloadManager),
		cookie:  &http.Cookie{Name: sessionCookie, Value: token}, server: provider,
	}
}

func huggingFaceRequest(t *testing.T, fixture huggingFaceFixture, method, path string, body any, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if authenticated {
		req.AddCookie(fixture.cookie)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	fixture.handler.ServeHTTP(w, req)
	return w
}

func decodeDownloadJob(t *testing.T, body []byte) downloads.Job {
	t.Helper()
	var job downloads.Job
	if err := json.Unmarshal(body, &job); err != nil {
		t.Fatalf("download job decode: %v body=%s", err, body)
	}
	return job
}

func waitHuggingFaceDownloadState(t *testing.T, fixture huggingFaceFixture, id, want string) downloads.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last downloads.Job
	var lastCode int
	var lastBody string
	for time.Now().Before(deadline) {
		w := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/downloads/"+id, nil, true)
		lastCode, lastBody = w.Code, w.Body.String()
		if w.Code == http.StatusOK {
			last = decodeDownloadJob(t, w.Body.Bytes())
			if last.State == want {
				return last
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("download %s did not reach %s: status=%d state=%s body=%s", id, want, lastCode, last.State, lastBody)
	return last
}

func TestHuggingFaceRequiresAuthenticationAndRoutesProvider(t *testing.T) {
	fixture := newHuggingFaceFixture(t)
	if got := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/search", nil, false).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", got)
	}
	w := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/search?q=demo&author=acme&sort=likes&limit=5", nil, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "acme/demo") {
		t.Fatalf("search status=%d body=%s", w.Code, w.Body.String())
	}
	w = huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/model?repo=acme%2Fdemo", nil, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "demo-Q4_K_M") {
		t.Fatalf("detail status=%d body=%s", w.Code, w.Body.String())
	}
	if got := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/model", nil, true).Code; got != http.StatusBadRequest {
		t.Fatalf("missing repo status = %d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/huggingface/search", nil, true).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("search method status = %d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/nope", nil, true).Code; got != http.StatusNotFound {
		t.Fatalf("unknown route status = %d", got)
	}
}

func TestHuggingFaceTokenCRUDNeverReturnsPlaintext(t *testing.T) {
	fixture := newHuggingFaceFixture(t)
	w := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/token", nil, true)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "hf_test") {
		t.Fatalf("initial status=%d body=%s", w.Code, w.Body.String())
	}
	w = huggingFaceRequest(t, fixture, http.MethodPut, "/api/v1/huggingface/token", map[string]string{"token": "hf_test_secret"}, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "configured") || strings.Contains(w.Body.String(), "hf_test_secret") {
		t.Fatalf("put status=%d body=%s", w.Code, w.Body.String())
	}
	w = huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/token", nil, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "hf_tes") || strings.Contains(w.Body.String(), "hf_test_secret") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPut, "/api/v1/huggingface/token", map[string]string{"token": " "}, true).Code; got != http.StatusBadRequest {
		t.Fatalf("empty token status = %d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodDelete, "/api/v1/huggingface/token", nil, true).Code; got != http.StatusNoContent {
		t.Fatalf("delete token status = %d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/huggingface/token", nil, true).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("token method status = %d", got)
	}
}

func TestDownloadJobStateIgnoresFileLevelCompleted(t *testing.T) {
	downloading := []byte(`{"id":"j1","state":"DOWNLOADING","files":[{"path":"a.gguf","state":"COMPLETED"}]}`)
	completed := []byte(`{"id":"j1","state":"COMPLETED","files":[{"path":"a.gguf","state":"COMPLETED"}]}`)
	if !strings.Contains(string(downloading), downloads.StateCompleted) {
		t.Fatal("fixture must include COMPLETED so a substring wait would fire early")
	}
	decoded := decodeDownloadJob(t, downloading)
	if decoded.State != downloads.StateDownloading {
		t.Fatalf("job state = %q, want %q", decoded.State, downloads.StateDownloading)
	}

	var polls int
	fixture := huggingFaceFixture{
		handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			polls++
			w.Header().Set("Content-Type", "application/json")
			body := downloading
			if polls > 1 {
				body = completed
			}
			_, _ = w.Write(body)
		}),
		cookie: &http.Cookie{Name: sessionCookie, Value: "unused"},
	}
	got := waitHuggingFaceDownloadState(t, fixture, "j1", downloads.StateCompleted)
	if polls < 2 {
		t.Fatalf("wait returned after %d poll(s); file-level COMPLETED must not complete the wait", polls)
	}
	if got.ID != "j1" || got.State != downloads.StateCompleted {
		t.Fatalf("got %+v", got)
	}
}

func TestHuggingFaceDownloadLifecycleRoutes(t *testing.T) {
	fixture := newHuggingFaceFixture(t)
	w := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/model?repo=acme%2Fdemo", nil, true)
	var detail huggingface.ModelDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || len(detail.Artifacts) != 1 {
		t.Fatalf("detail decode: %+v %v", detail, err)
	}
	w = huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads", map[string]string{"repo_id": "acme/demo", "artifact_id": "missing"}, true)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing artifact status=%d body=%s", w.Code, w.Body.String())
	}
	w = huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads", map[string]string{"repo_id": "acme/demo", "artifact_id": detail.Artifacts[0].ID}, true)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var job downloads.Job
	if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil || job.ID == "" {
		t.Fatalf("job decode: %+v %v", job, err)
	}
	job = waitHuggingFaceDownloadState(t, fixture, job.ID, downloads.StateCompleted)
	w = huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/downloads", nil, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), job.ID) {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads/"+job.ID+"/cancel", nil, true).Code; got != http.StatusNoContent {
		t.Fatalf("cancel completed status=%d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads/"+job.ID+"/retry", nil, true).Code; got != http.StatusBadRequest {
		t.Fatalf("retry completed status=%d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/downloads/missing", nil, true).Code; got != http.StatusNotFound {
		t.Fatalf("missing download status=%d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/downloads/missing/action", nil, true).Code; got != http.StatusNotFound {
		t.Fatalf("invalid action status=%d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPut, "/api/v1/downloads", nil, true).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("collection method status=%d", got)
	}
}
