package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

type apiFixture struct {
	server *Server
	auth   *auth.Service
	models *models.Service
	dbExec func(string, ...any)
	dir    string
}

func newAPIFixture(t *testing.T, profile func() (llamacpp.Profile, error)) *apiFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	modelsDir := filepath.Join(root, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := auth.New(db, time.Hour)
	m := models.New(db, modelsDir)
	sup := supervisor.New(filepath.Join(root, "missing-llama"), "127.0.0.1", 31000, 100*time.Millisecond)
	l := lifecycle.New(m, sup)
	if profile == nil {
		profile = func() (llamacpp.Profile, error) {
			return llamacpp.Profile{Path: "/app/llama-server", Version: "test", Fingerprint: "abc", Options: []llamacpp.Option{
				{Key: "ctx-size", ValueHint: "N", Kind: "integer"},
				{Key: "threads", ValueHint: "N", Kind: "integer"},
				{Key: "flash-attn", Kind: "boolean"},
				{Key: "cache-type-k", ValueHint: "<f16|q8_0>", Kind: "enum", Choices: []string{"f16", "q8_0"}},
				{Key: "chat-template", ValueHint: "STRING", Kind: "string"},
				{Key: "port", ValueHint: "N", Kind: "integer"},
			}}, nil
		}
	}
	return &apiFixture{server: New(m, l, profile), auth: a, models: m, dir: modelsDir, dbExec: func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}}
}

func doRequest(t *testing.T, h http.Handler, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else if raw, ok := body.([]byte); ok {
		reader = bytes.NewReader(raw)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	r := httptest.NewRequest(method, path, reader)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func bootstrapAndLogin(t *testing.T, f *apiFixture) *http.Cookie {
	t.Helper()
	ctx := t.Context()
	required, err := f.auth.BootstrapRequired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if required {
		if _, err := f.auth.Bootstrap(ctx, "admin", "correct-horse-battery"); err != nil {
			t.Fatal(err)
		}
	}
	token, _, _, err := f.auth.LoginWithMetadata(ctx, "admin", "correct-horse-battery", "127.0.0.1", "api-test")
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: token}
}

func createModel(t *testing.T, f *apiFixture, cookie *http.Cookie) models.Model {
	t.Helper()
	path := filepath.Join(f.dir, "api-Q4_K_M.gguf")
	if err := os.WriteFile(path, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "API Model", "gguf_path": path, "options": map[string]string{"ctx-size": "1024"},
	}, cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("create model status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Model models.Model `json:"model"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Model.ID == "" {
		t.Fatalf("missing model in create response: %s", w.Body.String())
	}
	return response.Model
}

func TestCoreHealthAndModelProfileRoutes(t *testing.T) {
	f := newAPIFixture(t, nil)
	w := doRequest(t, f.server, http.MethodGet, "/api/v1/health/", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health=%d", w.Code)
	}
	m := createModel(t, f, nil)
	nested := filepath.Join(f.dir, "Qwen", "coder")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "spare-Q8_0.gguf"), []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/models/available", nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"path":"Qwen/coder/spare-Q8_0.gguf"`) || strings.Contains(w.Body.String(), "/models/") {
		t.Fatalf("available models=%d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/v1/models", 200},
		{"/api/v1/models/" + m.ID, 200},
		{"/api/v1/models/" + m.ID + "/runtime", 200},
		{"/api/v1/models/" + m.ID + "/options", 200},
		{"/api/v1/llamacpp/profile", 200},
		{"/api/v1/instances/missing/logs", 404},
		{"/api/v1/artifacts", 404},
	} {
		w := doRequest(t, f.server, http.MethodGet, tc.path, nil, nil)
		if w.Code != tc.want {
			t.Fatalf("GET %s=%d body=%s", tc.path, w.Code, w.Body.String())
		}
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/models/"+m.ID+"/start", nil, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("start without instance=%d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/models/"+m.ID+"/stop", nil, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("stop=%d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.server, http.MethodDelete, "/api/v1/models/"+m.ID, nil, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete=%d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/models/"+m.ID, nil, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("deleted get=%d", w.Code)
	}
}

func TestCoreMethodsNotFoundAndProfileUnavailable(t *testing.T) {
	f := newAPIFixture(t, func() (llamacpp.Profile, error) { return llamacpp.Profile{}, errors.New("no llama") })
	w := doRequest(t, f.server, http.MethodPost, "/api/v1/artifacts/register", map[string]string{"path": "x"}, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("removed artifact route=%d", w.Code)
	}
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/llamacpp/profile", nil, nil)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "no llama") {
		t.Fatalf("profile unavailable=%d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		method string
		path   string
		body   any
		want   int
	}{
		{http.MethodPost, "/api/v1/models/available", nil, 405},
		{http.MethodPatch, "/api/v1/models/missing", map[string]any{}, 405},
		{http.MethodGet, "/api/v1/models/missing", nil, 404},
		{http.MethodPut, "/api/v1/models/missing", map[string]any{"name": "x"}, 404},
		{http.MethodGet, "/api/v1/models/missing/options", nil, 404},
		{http.MethodGet, "/api/v1/models/missing/runtime", nil, 404},
		{http.MethodGet, "/api/v1/models/missing/unknown", nil, 404},
		{http.MethodGet, "/api/v1/instances/missing", nil, 404},
		{http.MethodPost, "/api/v1/instances/missing/start", nil, 404},
		{http.MethodGet, "/api/v1/instances/missing/runtime", nil, 404},
		{http.MethodGet, "/api/v1/instances/missing/options", nil, 404},
		{http.MethodPost, "/api/v1/instances", map[string]any{}, 400},
		{http.MethodGet, "/api/v1/nope", nil, 404},
	} {
		w := doRequest(t, f.server, tc.method, tc.path, tc.body, nil)
		if w.Code != tc.want {
			t.Fatalf("%s %s=%d want=%d body=%s", tc.method, tc.path, w.Code, tc.want, w.Body.String())
		}
	}
}
