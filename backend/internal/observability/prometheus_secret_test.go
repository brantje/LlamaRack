package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/settings"
)

func TestMetricsHandlerUsesEncryptedPrometheusToken(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := huggingface.NewSecretStore(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.SetPrometheusToken(ctx, store, "metrics-secret"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	h := NewMetricsHandler(s, func(requestCtx context.Context) string {
		value, resolveErr := settings.ResolvePrometheusToken(requestCtx, store)
		if resolveErr != nil {
			return ""
		}
		return value
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth=%d", w.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.Header.Set("Authorization", "Bearer metrics-secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("auth=%d body=%s", w.Code, w.Body.String())
	}
}
