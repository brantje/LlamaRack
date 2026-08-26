package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func TestPersistenceFailuresBecomeHTTPErrorResponses(t *testing.T) {
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
	a := auth.New(db, time.Hour)
	m := models.New(db, modelsDir)
	l := lifecycle.New(m, supervisor.New(filepath.Join(root, "missing"), "127.0.0.1", 37000, time.Millisecond))
	s := New(a, m, l, func() (llamacpp.Profile, error) { return llamacpp.Profile{}, nil })
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	user := auth.User{ID: 1, Username: "admin", Enabled: true}

	for _, tc := range []struct {
		method, path string
		body         any
		want         int
	}{
		{http.MethodGet, "/api/v1/models", nil, 500},
		{http.MethodGet, "/api/v1/api-keys", nil, 500},
		{http.MethodPost, "/api/v1/api-keys", map[string]string{"name": "x"}, 500},
		{http.MethodPost, "/api/v1/api-keys/id/revoke", nil, 500},
	} {
		w := doRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.authenticated(w, r, tc.path, user) }), tc.method, tc.path, tc.body, nil)
		if w.Code != tc.want {
			t.Fatalf("%s %s=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/v1/models/id", 404},
		{http.MethodDelete, "/api/v1/models/id", 500},
		{http.MethodPost, "/api/v1/models/id/start", 503},
		{http.MethodPost, "/api/v1/models/id/stop", 500},
		{http.MethodGet, "/api/v1/models/id/runtime", 500},
		{http.MethodGet, "/api/v1/models/id/options", 500},
	} {
		w := doRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.modelRoute(w, r, tc.path) }), tc.method, tc.path, nil, nil)
		if w.Code != tc.want {
			t.Fatalf("%s %s=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	w := doRequest(t, s, http.MethodGet, "/api/v1/auth/bootstrap", nil, nil)
	if w.Code != 500 {
		t.Fatalf("bootstrap state closed DB=%d", w.Code)
	}
	w = doRequest(t, s, http.MethodPost, "/api/v1/auth/bootstrap", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil)
	if w.Code != 400 {
		t.Fatalf("bootstrap closed DB=%d", w.Code)
	}
	w = doRequest(t, s, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil)
	if w.Code != 401 {
		t.Fatalf("login closed DB=%d", w.Code)
	}
}
