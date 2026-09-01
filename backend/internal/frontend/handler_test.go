package frontend

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerServesFilesAndSPAFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "_nuxt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>app-shell</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "_nuxt", "app.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := New(root)
	cases := []struct {
		method string
		path   string
		want   int
		body   string
	}{
		{http.MethodGet, "/", http.StatusOK, "app-shell"},
		{http.MethodGet, "/models", http.StatusOK, "app-shell"},
		{http.MethodGet, "/models/discover/example", http.StatusOK, "app-shell"},
		{http.MethodGet, "/_nuxt/app.js", http.StatusOK, "console.log"},
		{http.MethodGet, "/_nuxt/missing.js", http.StatusNotFound, ""},
		{http.MethodGet, "/missing.css", http.StatusNotFound, ""},
		{http.MethodPost, "/models", http.StatusMethodNotAllowed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, "http://manager.test"+tc.path, nil)
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d", w.Code, tc.want)
			}
			if tc.body != "" && !strings.Contains(w.Body.String(), tc.body) {
				t.Fatalf("body=%q missing %q", w.Body.String(), tc.body)
			}
		})
	}
}

func TestHandlerReturnsNotFoundWithoutIndex(t *testing.T) {
	w := httptest.NewRecorder()
	New(t.TempDir()).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://manager.test/models", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want=%d", w.Code, http.StatusNotFound)
	}
}
