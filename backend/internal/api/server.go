package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

const sessionCookie = "lcm_session"

type Server struct {
	auth      *auth.Service
	models    *models.Service
	lifecycle *lifecycle.Service
	profile   func() (llamacpp.Profile, error)
}

func New(a *auth.Service, m *models.Service, l *lifecycle.Service, p func() (llamacpp.Profile, error)) *Server {
	return &Server{auth: a, models: m, lifecycle: l, profile: p}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}
	switch {
	case path == "/api/v1/health" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]string{"status": "ok"})
	case path == "/api/v1/auth/bootstrap" && r.Method == http.MethodGet:
		required, err := s.auth.BootstrapRequired(r.Context())
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"required": required})
	case path == "/api/v1/auth/bootstrap" && r.Method == http.MethodPost:
		s.bootstrap(w, r)
	case path == "/api/v1/auth/login" && r.Method == http.MethodPost:
		s.login(w, r)
	case path == "/api/v1/auth/logout" && r.Method == http.MethodPost:
		s.logout(w, r)
	default:
		user, ok := s.requireUser(w, r)
		if !ok {
			return
		}
		s.authenticated(w, r, path, user)
	}
}

func (s *Server) authenticated(w http.ResponseWriter, r *http.Request, path string, user auth.User) {
	switch {
	case path == "/api/v1/me" && r.Method == http.MethodGet:
		writeJSON(w, 200, user)
	case path == "/api/v1/models" && r.Method == http.MethodGet:
		items, err := s.models.List(r.Context())
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, items)
	case path == "/api/v1/models" && r.Method == http.MethodPost:
		if !requireOperate(w, user) {
			return
		}
		var in models.CreateModelInput
		if !decode(w, r, &in) {
			return
		}
		item, err := s.models.Create(r.Context(), in)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 201, item)
	case path == "/api/v1/artifacts" && r.Method == http.MethodGet:
		items, err := s.models.ListArtifacts(r.Context())
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, items)
	case path == "/api/v1/artifacts/register" && r.Method == http.MethodPost:
		if !requireOperate(w, user) {
			return
		}
		var in struct {
			Path        string `json:"path"`
			DisplayName string `json:"display_name"`
		}
		if !decode(w, r, &in) {
			return
		}
		item, err := s.models.RegisterArtifact(r.Context(), in.Path, in.DisplayName)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 201, item)
	case path == "/api/v1/llamacpp/profile" && r.Method == http.MethodGet:
		p, err := s.profile()
		if err != nil {
			writeJSON(w, 503, map[string]any{"available": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"available": true, "profile": p})
	case path == "/api/v1/api-keys" && r.Method == http.MethodGet:
		if !auth.IsAdmin(user.Role) {
			writeForbidden(w)
			return
		}
		items, err := s.auth.ListAPIKeys(r.Context())
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, items)
	case path == "/api/v1/api-keys" && r.Method == http.MethodPost:
		if !auth.IsAdmin(user.Role) {
			writeForbidden(w)
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &in) {
			return
		}
		key, secret, err := s.auth.CreateAPIKey(r.Context(), in.Name)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 201, map[string]any{"key": key, "secret": secret})
	case strings.HasPrefix(path, "/api/v1/api-keys/"):
		s.apiKeyRoute(w, r, path, user)
	case strings.HasPrefix(path, "/api/v1/models/"):
		s.modelRoute(w, r, path, user)
	case strings.HasPrefix(path, "/api/v1/instances/") && strings.HasSuffix(path, "/logs/stream") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/instances/"), "/logs/stream")
		s.streamLogs(w, r, id)
	case strings.HasPrefix(path, "/api/v1/instances/") && strings.HasSuffix(path, "/logs") && r.Method == http.MethodGet:
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/instances/"), "/logs")
		writeJSON(w, 200, map[string]any{"lines": s.lifecycle.Logs(id)})
	default:
		writeJSON(w, 404, map[string]string{"error": "not found"})
	}
}

func (s *Server) apiKeyRoute(w http.ResponseWriter, r *http.Request, path string, user auth.User) {
	if !auth.IsAdmin(user.Role) {
		writeForbidden(w)
		return
	}
	rest := strings.TrimPrefix(path, "/api/v1/api-keys/")
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "revoke" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := s.auth.DeleteAPIKey(r.Context(), parts[0]); err != nil {
			writeAPIKeyMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) != 1 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	id := parts[0]
	switch r.Method {
	case http.MethodPatch:
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Enabled == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enabled is required"})
			return
		}
		if err := s.auth.SetAPIKeyEnabled(r.Context(), id, *in.Enabled); err != nil {
			writeAPIKeyMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := s.auth.DeleteAPIKey(r.Context(), id); err != nil {
			writeAPIKeyMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeAPIKeyMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "api key not found"})
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]string{"error": "streaming unsupported"})
		return
	}
	snapshot, events, cancel := s.lifecycle.SubscribeLogs(id)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeLine := func(line string) bool {
		payload, err := json.Marshal(line)
		if err != nil {
			return false
		}
		if _, err := w.Write([]byte("data: ")); err != nil {
			return false
		}
		if _, err := w.Write(payload); err != nil {
			return false
		}
		_, err = w.Write([]byte("\n\n"))
		return err == nil
	}
	for _, line := range snapshot {
		if !writeLine(line) {
			return
		}
	}
	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case line, open := <-events:
			if !open || !writeLine(line) {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) modelRoute(w http.ResponseWriter, r *http.Request, path string, user auth.User) {
	rest := strings.TrimPrefix(path, "/api/v1/models/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			item, err := s.models.GetByID(r.Context(), id)
			if err != nil {
				writeErr(w, 404, err)
				return
			}
			writeJSON(w, 200, item)
		case http.MethodDelete:
			if !requireOperate(w, user) {
				return
			}
			if err := s.lifecycle.StopModel(r.Context(), id); err != nil {
				writeErr(w, 500, err)
				return
			}
			if err := s.models.Delete(r.Context(), id); err != nil {
				writeErr(w, 500, err)
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
		return
	}
	if len(parts) != 2 {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	switch parts[1] {
	case "start":
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if !requireOperate(w, user) {
			return
		}
		_, err := s.lifecycle.StartModel(r.Context(), id)
		if err != nil {
			writeErr(w, 503, err)
			return
		}
		items, _ := s.lifecycle.Runtime(r.Context(), id)
		writeJSON(w, 202, items)
	case "stop":
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if !requireOperate(w, user) {
			return
		}
		if err := s.lifecycle.StopModel(r.Context(), id); err != nil {
			writeErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	case "runtime":
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		items, err := s.lifecycle.Runtime(r.Context(), id)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, items)
	case "options":
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		items, err := s.models.Options(r.Context(), id)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, items)
	default:
		writeJSON(w, 404, map[string]string{"error": "not found"})
	}
}

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.auth.Bootstrap(r.Context(), in.Username, in.Password)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 201, u)
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	token, u, err := s.auth.Login(r.Context(), in.Username, in.Password)
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "invalid username or password"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int((24 * time.Hour).Seconds())})
	writeJSON(w, 200, u)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.auth.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(204)
}
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "authentication required"})
		return auth.User{}, false
	}
	u, err := s.auth.SessionUser(r.Context(), c.Value)
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "authentication required"})
		return auth.User{}, false
	}
	return u, true
}
func requireOperate(w http.ResponseWriter, u auth.User) bool {
	if !auth.CanOperate(u.Role) {
		writeForbidden(w)
		return false
	}
	return true
}
func writeForbidden(w http.ResponseWriter) { writeJSON(w, 403, map[string]string{"error": "forbidden"}) }
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, 400, err)
		return false
	}
	return true
}
func writeErr(w http.ResponseWriter, status int, err error) {
	if err == nil {
		err = errors.New("unknown error")
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
