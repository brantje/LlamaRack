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

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
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
			return llamacpp.Profile{Path: "/app/llama-server", Version: "test", Fingerprint: "abc"}, nil
		}
	}
	return &apiFixture{
		server: New(a, m, l, profile), auth: a, models: m, dir: modelsDir,
		dbExec: func(q string, args ...any) {
			t.Helper()
			if _, err := db.ExecContext(ctx, q, args...); err != nil {
				t.Fatal(err)
			}
		},
	}
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

func bootstrapAndLogin(t *testing.T, f *apiFixture, role string) *http.Cookie {
	t.Helper()
	w := doRequest(t, f.server, http.MethodPost, "/api/v1/auth/bootstrap", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("bootstrap status=%d body=%s", w.Code, w.Body.String())
	}
	if role != "admin" {
		f.dbExec("UPDATE users SET role=? WHERE username='admin'", role)
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("missing session cookie")
	return nil
}

func createArtifactAndModel(t *testing.T, f *apiFixture, cookie *http.Cookie) (models.Artifact, models.Model) {
	t.Helper()
	path := filepath.Join(f.dir, "api-Q4_K_M.gguf")
	if err := os.WriteFile(path, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := doRequest(t, f.server, http.MethodPost, "/api/v1/artifacts/register", map[string]string{"path": path, "display_name": "API Model"}, cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", w.Code, w.Body.String())
	}
	var a models.Artifact
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]any{"model_id": "api-model", "artifact_id": a.ID, "options": map[string]string{"ctx-size": "1024"}}, cookie)
	if w.Code != http.StatusCreated {
		t.Fatalf("create model status=%d body=%s", w.Code, w.Body.String())
	}
	var m models.Model
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return a, m
}

func TestPublicAuthAndSessionRoutes(t *testing.T) {
	f := newAPIFixture(t, nil)
	w := doRequest(t, f.server, http.MethodGet, "/api/v1/health/", nil, nil)
	if w.Code != 200 {
		t.Fatalf("health=%d", w.Code)
	}
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/auth/bootstrap", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "true") {
		t.Fatalf("bootstrap state=%d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/auth/bootstrap", []byte(`{"username":"x","password":"short"}`), nil)
	if w.Code != 400 {
		t.Fatalf("invalid bootstrap=%d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/auth/bootstrap", []byte(`{"username":"admin","password":"correct-horse-battery","extra":true}`), nil)
	if w.Code != 400 {
		t.Fatalf("unknown field=%d %s", w.Code, w.Body.String())
	}

	cookie := bootstrapAndLogin(t, f, "admin")
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/me", nil, nil)
	if w.Code != 401 {
		t.Fatalf("missing auth=%d", w.Code)
	}
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/me", nil, cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "admin") {
		t.Fatalf("me=%d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong"}, nil)
	if w.Code != 401 {
		t.Fatalf("bad login=%d", w.Code)
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/auth/logout", nil, cookie)
	if w.Code != 204 {
		t.Fatalf("logout=%d", w.Code)
	}
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/me", nil, cookie)
	if w.Code != 401 {
		t.Fatalf("revoked session=%d", w.Code)
	}
}

func TestAdminModelArtifactProfileAndAPIKeyRoutes(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f, "admin")
	a, m := createArtifactAndModel(t, f, cookie)

	for _, tc := range []struct{ path string; want int }{
		{"/api/v1/artifacts", 200},
		{"/api/v1/models", 200},
		{"/api/v1/models/" + m.ID, 200},
		{"/api/v1/models/" + m.ID + "/runtime", 200},
		{"/api/v1/models/" + m.ID + "/options", 200},
		{"/api/v1/llamacpp/profile", 200},
		{"/api/v1/instances/missing/logs", 200},
	} {
		w := doRequest(t, f.server, http.MethodGet, tc.path, nil, cookie)
		if w.Code != tc.want {
			t.Fatalf("GET %s=%d body=%s", tc.path, w.Code, w.Body.String())
		}
	}
	_ = a

	w := doRequest(t, f.server, http.MethodPost, "/api/v1/api-keys", map[string]string{"name": "sdk"}, cookie)
	if w.Code != 201 || !strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("create key=%d %s", w.Code, w.Body.String())
	}
	var created struct {
		Key auth.APIKey `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/api-keys", nil, cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "sdk") {
		t.Fatalf("list keys=%d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/api-keys/"+created.Key.ID+"/revoke", nil, cookie)
	if w.Code != 204 {
		t.Fatalf("revoke=%d %s", w.Code, w.Body.String())
	}

	w = doRequest(t, f.server, http.MethodPost, "/api/v1/models/"+m.ID+"/start", nil, cookie)
	if w.Code != 503 {
		t.Fatalf("start without binary=%d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/models/"+m.ID+"/stop", nil, cookie)
	if w.Code != 204 {
		t.Fatalf("stop=%d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.server, http.MethodDelete, "/api/v1/models/"+m.ID, nil, cookie)
	if w.Code != 204 {
		t.Fatalf("delete=%d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/models/"+m.ID, nil, cookie)
	if w.Code != 404 {
		t.Fatalf("deleted get=%d", w.Code)
	}
}

func TestAuthorizationMethodsNotFoundAndProfileUnavailable(t *testing.T) {
	f := newAPIFixture(t, func() (llamacpp.Profile, error) { return llamacpp.Profile{}, errors.New("no llama") })
	cookie := bootstrapAndLogin(t, f, "readonly")

	w := doRequest(t, f.server, http.MethodPost, "/api/v1/artifacts/register", map[string]string{"path": "x"}, cookie)
	if w.Code != 403 {
		t.Fatalf("readonly artifact=%d", w.Code)
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]string{}, cookie)
	if w.Code != 403 {
		t.Fatalf("readonly model=%d", w.Code)
	}
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/api-keys", nil, cookie)
	if w.Code != 403 {
		t.Fatalf("readonly keys=%d", w.Code)
	}
	w = doRequest(t, f.server, http.MethodPost, "/api/v1/api-keys", map[string]string{}, cookie)
	if w.Code != 403 {
		t.Fatalf("readonly create key=%d", w.Code)
	}
	w = doRequest(t, f.server, http.MethodGet, "/api/v1/llamacpp/profile", nil, cookie)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "no llama") {
		t.Fatalf("profile unavailable=%d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct{ method, path string; want int }{
		{http.MethodPatch, "/api/v1/models/missing", 405},
		{http.MethodGet, "/api/v1/models/missing/x/y", 404},
		{http.MethodGet, "/api/v1/models/missing/start", 405},
		{http.MethodGet, "/api/v1/models/missing/stop", 405},
		{http.MethodPost, "/api/v1/models/missing/runtime", 405},
		{http.MethodPost, "/api/v1/models/missing/options", 405},
		{http.MethodGet, "/api/v1/models/missing/unknown", 404},
		{http.MethodGet, "/api/v1/unknown", 404},
	} {
		w := doRequest(t, f.server, tc.method, tc.path, nil, cookie)
		if w.Code != tc.want {
			t.Fatalf("%s %s=%d want=%d body=%s", tc.method, tc.path, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestWriteHelpers(t *testing.T) {
	w := httptest.NewRecorder()
	writeErr(w, 418, nil)
	if w.Code != 418 || !strings.Contains(w.Body.String(), "unknown error") {
		t.Fatalf("writeErr=%d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	writeForbidden(w)
	if w.Code != 403 {
		t.Fatalf("forbidden=%d", w.Code)
	}
}
