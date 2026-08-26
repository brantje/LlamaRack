package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/instances"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
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
	case path == "/api/v1/models/available" && r.Method == http.MethodGet:
		items, err := s.models.AvailableGGUFs(r.Context())
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, items)
	case path == "/api/v1/models" && r.Method == http.MethodPost:
		s.createModel(w, r)
	case path == "/api/v1/instances" && r.Method == http.MethodGet:
		items, err := s.lifecycle.Instances().List(r.Context())
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, items)
	case path == "/api/v1/instances" && r.Method == http.MethodPost:
		var in instances.CreateInput
		if !decode(w, r, &in) {
			return
		}
		item, err := s.lifecycle.Instances().Create(r.Context(), in)
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
		items, err := s.auth.ListAPIKeys(r.Context())
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, items)
	case path == "/api/v1/api-keys" && r.Method == http.MethodPost:
		var in struct{ Name string `json:"name"` }
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
		s.apiKeyRoute(w, r, path)
	case strings.HasPrefix(path, "/api/v1/models/"):
		s.modelRoute(w, r, path)
	case strings.HasPrefix(path, "/api/v1/instances/"):
		s.instanceRoute(w, r, path)
	default:
		writeJSON(w, 404, map[string]string{"error": "not found"})
	}
}

func (s *Server) createModel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		models.CreateModelInput
		FirstInstance *struct {
			Name            string `json:"name"`
			AlwaysOn        bool   `json:"always_on"`
			Autoload        *bool  `json:"autoload_enabled,omitempty"`
			EvictionEnabled *bool  `json:"eviction_enabled,omitempty"`
			Start           bool   `json:"start"`
		} `json:"first_instance,omitempty"`
	}
	if !decode(w, r, &in) {
		return
	}
	model, err := s.models.Create(r.Context(), in.CreateModelInput)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if in.FirstInstance == nil {
		writeJSON(w, 201, map[string]any{"model": model})
		return
	}
	instance, err := s.lifecycle.Instances().Create(r.Context(), instances.CreateInput{
		ModelID: model.ID, Name: in.FirstInstance.Name, AlwaysOn: in.FirstInstance.AlwaysOn,
		Autoload: in.FirstInstance.Autoload, EvictionEnabled: in.FirstInstance.EvictionEnabled,
	})
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error(), "model": model})
		return
	}
	response := map[string]any{"model": model, "instance": instance}
	if in.FirstInstance.Start {
		if _, err := s.lifecycle.StartInstance(r.Context(), instance.ID); err != nil {
			// Both durable records intentionally survive a launch failure.
			response["start_error"] = err.Error()
		}
	}
	writeJSON(w, 201, response)
}

func (s *Server) apiKeyRoute(w http.ResponseWriter, r *http.Request, path string) {
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
		var in struct{ Enabled *bool `json:"enabled"` }
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

func (s *Server) modelRoute(w http.ResponseWriter, r *http.Request, path string) {
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
		case http.MethodPut:
			var in models.UpdateModelInput
			if !decode(w, r, &in) {
				return
			}
			item, err := s.models.Update(r.Context(), id, in)
			if err != nil {
				writeErr(w, 400, err)
				return
			}
			writeJSON(w, 200, item)
		case http.MethodDelete:
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
	// Compatibility endpoints retained for older clients during development.
	// The Phase 5.5 UI does not expose Model lifecycle controls.
	case "start":
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
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
	default:
		writeJSON(w, 404, map[string]string{"error": "not found"})
	}
}

func (s *Server) instanceRoute(w http.ResponseWriter, r *http.Request, path string) {
	rest := strings.TrimPrefix(path, "/api/v1/instances/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			item, err := s.lifecycle.Instances().Get(r.Context(), id)
			if err != nil {
				writeErr(w, 404, err)
				return
			}
			writeJSON(w, 200, item)
		case http.MethodPut:
			s.updateInstance(w, r, id)
		case http.MethodDelete:
			_ = s.lifecycle.StopInstance(r.Context(), id)
			if err := s.lifecycle.Instances().Delete(r.Context(), id); err != nil {
				writeErr(w, 404, err)
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
		if _, err := s.lifecycle.StartInstance(r.Context(), id); err != nil {
			writeErr(w, 503, err)
			return
		}
		rt, _ := s.lifecycle.RuntimeInstance(r.Context(), id)
		writeJSON(w, 202, rt)
	case "stop":
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if err := s.lifecycle.StopInstance(r.Context(), id); err != nil {
			writeErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	case "restart":
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if _, err := s.lifecycle.RestartInstance(r.Context(), id); err != nil {
			writeErr(w, 503, err)
			return
		}
		rt, _ := s.lifecycle.RuntimeInstance(r.Context(), id)
		writeJSON(w, 202, rt)
	case "kill":
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if err := s.lifecycle.KillInstance(r.Context(), id); err != nil {
			writeErr(w, 500, err)
			return
		}
		w.WriteHeader(204)
	case "duplicate":
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		item, err := s.lifecycle.Instances().Duplicate(r.Context(), id)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 201, item)
	case "runtime":
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		rt, err := s.lifecycle.RuntimeInstance(r.Context(), id)
		if err != nil {
			writeErr(w, 404, err)
			return
		}
		writeJSON(w, 200, rt)
	case "options":
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		items, err := s.lifecycle.Instances().Options(r.Context(), id)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, items)
	case "logs":
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		writeJSON(w, 200, map[string]any{"lines": s.lifecycle.Logs(id)})
	case "logs/stream":
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		s.streamLogs(w, r, id)
	default:
		writeJSON(w, 404, map[string]string{"error": "not found"})
	}
}

func (s *Server) updateInstance(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		instances.UpdateInput
		RestartRunning       bool `json:"restart_running"`
		ConfirmModelIDChange bool `json:"confirm_model_id_change"`
	}
	if !decode(w, r, &in) {
		return
	}
	current, err := s.lifecycle.Instances().Get(r.Context(), id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	newID := instances.Slugify(in.Name)
	if newID != current.ID && !in.ConfirmModelIDChange {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "renaming this Instance changes the OpenAI model ID; confirmation required"})
		return
	}
	rt, _ := s.lifecycle.RuntimeInstance(r.Context(), id)
	running := rt.State != supervisor.Unloaded && rt.State != supervisor.Failed
	if running && !in.RestartRunning {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "running Instance must restart to apply configuration; confirmation required"})
		return
	}
	if running {
		if err := s.lifecycle.StopInstance(r.Context(), id); err != nil {
			writeErr(w, 500, err)
			return
		}
	}
	item, err := s.lifecycle.Instances().Update(r.Context(), id, in.UpdateInput)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if running {
		if _, err := s.lifecycle.StartInstance(r.Context(), item.ID); err != nil {
			writeJSON(w, 503, map[string]any{"error": err.Error(), "instance": item})
			return
		}
	}
	writeJSON(w, 200, item)
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
