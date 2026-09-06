package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestCorePersistenceFailuresBecomeHTTPErrorResponses(t *testing.T) {
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
	m := models.New(db, modelsDir)
	l := lifecycle.New(m, supervisor.New(filepath.Join(root, "missing"), "127.0.0.1", 37000, time.Millisecond))
	s := New(m, l, func() (llamacpp.Profile, error) { return llamacpp.Profile{}, nil })
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	w := doRequest(t, s, http.MethodGet, "/api/v1/models", nil, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("models list=%d body=%s", w.Code, w.Body.String())
	}

	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/v1/models/id", 500},
		{http.MethodDelete, "/api/v1/models/id", 500},
		{http.MethodPost, "/api/v1/models/id/start", 500},
		{http.MethodPost, "/api/v1/models/id/stop", 500},
		{http.MethodGet, "/api/v1/models/id/runtime", 500},
		{http.MethodGet, "/api/v1/models/id/options", 500},
	} {
		w := doRequest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.modelRoute(w, r, tc.path)
		}), tc.method, tc.path, nil, nil)
		if w.Code != tc.want {
			t.Fatalf("%s %s=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
