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

	"github.com/brantje/llamacpp-manager/internal/auth"
	"github.com/brantje/llamacpp-manager/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/internal/models"
)

type Gateway struct {
	auth      *auth.Service
	models    *models.Service
	lifecycle *lifecycle.Service
	maxBody   int64
}

func New(authService *auth.Service, modelService *models.Service, lifecycleService *lifecycle.Service) *Gateway {
	return &Gateway{auth: authService, models: modelService, lifecycle: lifecycleService, maxBody: 32 << 20}
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
	if r.Method != http.MethodPost || !supportedPath(r.URL.Path) {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Unknown OpenAI-compatible endpoint")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, g.maxBody+1))
	if err != nil || int64(len(body)) > g.maxBody {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_body", "Invalid or oversized request body")
		return
	}
	_ = r.Body.Close()
	var envelope struct{ Model string `json:"model"` }
	if err := json.Unmarshal(body, &envelope); err != nil || strings.TrimSpace(envelope.Model) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model_required", "A model ID is required")
		return
	}

	endpoint, err := g.lifecycle.EnsureReady(r.Context(), envelope.Model)
	if err != nil {
		status, code := classifyAvailabilityError(err)
		writeError(w, status, "server_error", code, err.Error())
		return
	}
	target, err := url.Parse(endpoint)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "invalid_worker_endpoint", "Internal worker endpoint is invalid")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Del("Authorization")

	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
		req.Header.Del("Authorization")
	}
	proxy.FlushInterval = -1
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		writeError(w, http.StatusServiceUnavailable, "server_error", "backend_unavailable", "The model worker became unavailable")
	}
	proxy.ServeHTTP(w, r)
}

func (g *Gateway) listModels(w http.ResponseWriter, r *http.Request) {
	items, err := g.models.List(r.Context())
	if err != nil { writeError(w, 500, "server_error", "database_error", "Unable to list models"); return }
	type modelObject struct { ID string `json:"id"`; Object string `json:"object"`; Created int64 `json:"created"`; OwnedBy string `json:"owned_by"` }
	data := make([]modelObject,0,len(items))
	for _, item := range items {
		if item.Enabled { data=append(data,modelObject{ID:item.ModelID,Object:"model",Created:time.Now().Unix(),OwnedBy:"llamacpp-manager"}) }
	}
	writeJSON(w,http.StatusOK,map[string]any{"object":"list","data":data})
}

func (g *Gateway) authenticate(ctx context.Context, header string) error {
	const prefix="Bearer "
	if !strings.HasPrefix(header,prefix){return errors.New("missing bearer token")}
	return g.auth.AuthenticateAPIKey(ctx,strings.TrimSpace(strings.TrimPrefix(header,prefix)))
}

func supportedPath(path string) bool {
	switch path { case "/v1/chat/completions","/v1/completions","/v1/responses","/v1/embeddings": return true; default:return false }
}

func classifyAvailabilityError(err error)(int,string){
	message:=strings.ToLower(err.Error())
	switch {
	case strings.Contains(message,"not found")||strings.Contains(message,"no rows"): return http.StatusNotFound,"model_not_found"
	case strings.Contains(message,"autoload is disabled")||strings.Contains(message,"disabled"): return http.StatusServiceUnavailable,"model_unavailable"
	case strings.Contains(message,"timeout")||errors.Is(err,context.DeadlineExceeded): return http.StatusGatewayTimeout,"model_startup_timeout"
	case strings.Contains(message,"resource"): return http.StatusServiceUnavailable,"insufficient_resources"
	default:return http.StatusServiceUnavailable,"backend_unavailable"
	}
}

func writeError(w http.ResponseWriter,status int,typ,code,message string){writeJSON(w,status,map[string]any{"error":map[string]any{"message":message,"type":typ,"param":nil,"code":code}})}
func writeJSON(w http.ResponseWriter,status int,value any){w.Header().Set("Content-Type","application/json");w.WriteHeader(status);_ = json.NewEncoder(w).Encode(value)}
