package frontend

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Handler serves a generated Nuxt SPA from disk. Existing files are served
// directly, while browser routes fall back to index.html for client-side routing.
type Handler struct {
	root string
}

func New(root string) *Handler {
	return &Handler{root: root}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Anchor the request path at / before cleaning it so traversal attempts
	// can never escape the configured frontend root.
	cleanPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if cleanPath == "" || cleanPath == "." {
		cleanPath = "index.html"
	}
	candidate := filepath.Join(h.root, filepath.FromSlash(cleanPath))
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		http.ServeFile(w, r, candidate)
		return
	}

	// Missing static assets must remain a real 404; returning index.html here
	// would make browsers try to parse HTML as JavaScript, CSS, or an image.
	if strings.HasPrefix(r.URL.Path, "/_nuxt/") || path.Ext(cleanPath) != "" {
		http.NotFound(w, r)
		return
	}

	index := filepath.Join(h.root, "index.html")
	if _, err := os.Stat(index); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, index)
}
