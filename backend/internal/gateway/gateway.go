package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

type Gateway struct {
	auth      *auth.Service
	models    *models.Service
	lifecycle *lifecycle.Service
}

func New(a *auth.Service, m *models.Service, l *lifecycle.Service) *Gateway {
	return &Gateway{auth: a, models: m, lifecycle: l}
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := g.authenticate(r.Context(), r.Header.Get("Authorization")); err != nil {
		writeError(w, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "Invalid API key")
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
		g.listModels(w, r)
		return
	}
	if r.Method != http.MethodPost || !supported(r.URL.Path) {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Unknown OpenAI-compatible endpoint")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_body", "Invalid request body")
		return
	}
	var envelope struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || strings.TrimSpace(envelope.Model) == "" {
		writeError(w, 400, "invalid_request_error", "model_required", "A model ID is required")
		return
	}
	endpoint, release, err := g.lifecycle.Acquire(r.Context(), envelope.Model)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "server_error", "model_unavailable", err.Error())
		return
	}
	defer release()

	target, err := url.Parse(endpoint)
	if err != nil {
		writeError(w, 500, "server_error", "invalid_worker_endpoint", "Invalid worker endpoint")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Authorization")
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(req *http.Request) {
		original(req)
		req.Host = target.Host
		req.Header.Del("Authorization")
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		writeError(w, 503, "server_error", "backend_unavailable", "Model worker unavailable")
	}
	proxy.ServeHTTP(w, r)
}

func (g *Gateway) authenticate(ctx context.Context, header string) error {
	if !strings.HasPrefix(header, "Bearer ") {
		return errors.New("missing bearer token")
	}
	return g.auth.AuthenticateAPIKey(ctx, strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
}

func (g *Gateway) listModels(w http.ResponseWriter, r *http.Request) {
	items, err := g.models.List(r.Context())
	if err != nil {
		writeError(w, 500, "server_error", "database_error", "Unable to list models")
		return
	}
	type item struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	out := make([]item, 0, len(items))
	for _, m := range items {
		if m.Enabled {
			out = append(out, item{ID: m.PublicID, Object: "model", Created: time.Now().Unix(), OwnedBy: "llamacpp-manager"})
		}
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": out})
}

func supported(path string) bool {
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/embeddings":
		return true
	}
	return false
}

func writeError(w http.ResponseWriter, status int, typ, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": typ, "param": nil, "code": code}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
