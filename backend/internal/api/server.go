package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

type Server struct {
	models    *models.Service
	lifecycle *lifecycle.Service
	profile   func() (llamacpp.Profile, error)
}

func New(m *models.Service, l *lifecycle.Service, p func() (llamacpp.Profile, error)) *Server {
	return &Server{models: m, lifecycle: l, profile: p}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}
	if path == "/api/v1/health" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	s.routes(w, r, path)
}

func (s *Server) routes(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case path == "/api/v1/models" && r.Method == http.MethodGet:
		items, err := s.models.List(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case path == "/api/v1/models/available" && r.Method == http.MethodGet:
		items, err := s.models.AvailableGGUFs(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case path == "/api/v1/models/available":
		w.WriteHeader(http.StatusMethodNotAllowed)
	case path == "/api/v1/models" && r.Method == http.MethodPost:
		s.createModel(w, r)
	case path == "/api/v1/instances" && r.Method == http.MethodGet:
		items, err := s.lifecycle.Instances().List(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case path == "/api/v1/instances" && r.Method == http.MethodPost:
		var in instances.CreateInput
		if !decode(w, r, &in) {
			return
		}
		validated, err := s.validateLlamaOptions(in.Options)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		in.Options = validated
		item, err := s.lifecycle.Instances().Create(r.Context(), in)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	case path == "/api/v1/llamacpp/profile" && r.Method == http.MethodGet:
		profile, err := s.profile()
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"available": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"available": true, "profile": profile})
	case strings.HasPrefix(path, "/api/v1/models/"):
		s.modelRoute(w, r, path)
	case strings.HasPrefix(path, "/api/v1/instances/"):
		s.instanceRoute(w, r, path)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (s *Server) validateLlamaOptions(options map[string]string) (map[string]string, error) {
	if options == nil {
		return nil, nil
	}
	if len(options) == 0 {
		return map[string]string{}, nil
	}
	profile, err := s.profile()
	if err != nil {
		return nil, fmt.Errorf("cannot validate llama.cpp options: %w", err)
	}
	return llamacpp.ValidateOptions(profile, options)
}

func (s *Server) createModel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		models.CreateModelInput
		FirstInstance *struct {
			Name            string `json:"name"`
			Slug            string `json:"slug,omitempty"`
			AlwaysOn        bool   `json:"always_on"`
			Autoload        *bool  `json:"autoload_enabled,omitempty"`
			EvictionEnabled *bool  `json:"eviction_enabled,omitempty"`
			Start           bool   `json:"start"`
		} `json:"first_instance,omitempty"`
	}
	if !decode(w, r, &in) {
		return
	}
	validated, err := s.validateLlamaOptions(in.Options)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.Options = validated
	model, err := s.models.Create(r.Context(), in.CreateModelInput)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if in.FirstInstance == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"model": model})
		return
	}
	instance, err := s.lifecycle.Instances().Create(r.Context(), instances.CreateInput{
		ModelID: model.ID, Name: in.FirstInstance.Name, Slug: in.FirstInstance.Slug, AlwaysOn: in.FirstInstance.AlwaysOn,
		Autoload: in.FirstInstance.Autoload, EvictionEnabled: in.FirstInstance.EvictionEnabled,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "model": model})
		return
	}
	response := map[string]any{"model": model, "instance": instance}
	if in.FirstInstance.Start {
		if _, err := s.lifecycle.StartInstance(r.Context(), instance.ID); err != nil {
			response["start_error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request, id string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
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
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !validModelRouteMethod(w, r.Method, parts) {
		return
	}
	model, err := s.resolveModelRoute(r, parts[0])
	if err != nil {
		writeResourceLookupError(w, "model", err)
		return
	}
	id := model.ID
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, model)
		case http.MethodPut:
			var in struct {
				models.UpdateModelInput
				ConfirmSlugChange bool `json:"confirm_slug_change"`
			}
			if !decode(w, r, &in) {
				return
			}
			validated, err := s.validateLlamaOptions(in.Options)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			in.Options = validated
			nextSlug := model.Slug
			if strings.TrimSpace(in.Slug) != "" {
				nextSlug = modelsSlug(in.Slug)
			}
			if nextSlug != model.Slug && !in.ConfirmSlugChange {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "changing this Model slug changes management URLs; confirmation required"})
				return
			}
			item, err := s.models.Update(r.Context(), id, in.UpdateModelInput)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			s.deleteModel(w, r, id)
		}
		return
	}
	switch parts[1] {
	case "options":
		items, err := s.models.Options(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case "start":
		_, err := s.lifecycle.StartModel(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
		items, _ := s.lifecycle.Runtime(r.Context(), id)
		writeJSON(w, http.StatusAccepted, items)
	case "stop":
		if err := s.lifecycle.StopModel(r.Context(), id); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "runtime":
		items, err := s.lifecycle.Runtime(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	}
}

func validModelRouteMethod(w http.ResponseWriter, method string, parts []string) bool {
	if len(parts) == 1 {
		if method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete {
			return true
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	if len(parts) != 2 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return false
	}
	required := ""
	switch parts[1] {
	case "options", "runtime":
		required = http.MethodGet
	case "start", "stop":
		required = http.MethodPost
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return false
	}
	if method != required {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func (s *Server) resolveModelRoute(r *http.Request, value string) (models.Model, error) {
	item, err := s.models.GetBySlug(r.Context(), value)
	if err == nil {
		return item, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return models.Model{}, err
	}
	// Transitional compatibility for old opaque-ID management links. Frontend
	// navigation always emits the canonical slug route.
	return s.models.GetByID(r.Context(), value)
}

func modelsSlug(value string) string { return instances.Slugify(value) }

func (s *Server) instanceRoute(w http.ResponseWriter, r *http.Request, path string) {
	rest := strings.TrimPrefix(path, "/api/v1/instances/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !validInstanceRouteMethod(w, r.Method, parts) {
		return
	}
	instance, err := s.lifecycle.Instances().GetBySlug(r.Context(), parts[0])
	if err != nil {
		writeResourceLookupError(w, "instance", err)
		return
	}
	id := instance.ID
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, instance)
		case http.MethodPut:
			s.updateInstance(w, r, instance)
		case http.MethodDelete:
			_ = s.lifecycle.StopInstance(r.Context(), id)
			if err := s.lifecycle.Instances().Delete(r.Context(), id); err != nil {
				writeResourceLookupError(w, "instance", err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}
	if len(parts) == 3 {
		s.streamLogs(w, r, id)
		return
	}
	switch parts[1] {
	case "start":
		if _, err := s.lifecycle.StartInstance(r.Context(), id); err != nil {
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
		runtime, _ := s.lifecycle.RuntimeInstance(r.Context(), id)
		writeJSON(w, http.StatusAccepted, runtime)
	case "stop":
		if err := s.lifecycle.StopInstance(r.Context(), id); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "restart":
		if _, err := s.lifecycle.RestartInstance(r.Context(), id); err != nil {
			writeErr(w, http.StatusServiceUnavailable, err)
			return
		}
		runtime, _ := s.lifecycle.RuntimeInstance(r.Context(), id)
		writeJSON(w, http.StatusAccepted, runtime)
	case "kill":
		if err := s.lifecycle.KillInstance(r.Context(), id); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "duplicate":
		item, err := s.lifecycle.Instances().Duplicate(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, item)
	case "runtime":
		runtime, err := s.lifecycle.RuntimeInstance(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, runtime)
	case "options":
		items, err := s.lifecycle.Instances().Options(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case "logs":
		writeJSON(w, http.StatusOK, map[string]any{"lines": s.lifecycle.Logs(id)})
	}
}

func validInstanceRouteMethod(w http.ResponseWriter, method string, parts []string) bool {
	if len(parts) == 1 {
		if method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete {
			return true
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	if len(parts) == 3 {
		if parts[1] != "logs" || parts[2] != "stream" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return false
		}
		if method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return false
		}
		return true
	}
	if len(parts) != 2 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return false
	}
	required := ""
	switch parts[1] {
	case "runtime", "options", "logs":
		required = http.MethodGet
	case "start", "stop", "restart", "kill", "duplicate":
		required = http.MethodPost
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return false
	}
	if method != required {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func writeResourceLookupError(w http.ResponseWriter, resource string, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": resource + " not found"})
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

func (s *Server) updateInstance(w http.ResponseWriter, r *http.Request, current instances.Instance) {
	var in struct {
		instances.UpdateInput
		RestartRunning       bool `json:"restart_running"`
		ConfirmModelIDChange bool `json:"confirm_model_id_change"`
		ConfirmSlugChange    bool `json:"confirm_slug_change"`
	}
	if !decode(w, r, &in) {
		return
	}
	validated, err := s.validateLlamaOptions(in.Options)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.Options = validated
	nextSlug := current.Slug
	if strings.TrimSpace(in.Slug) != "" {
		nextSlug = instances.Slugify(in.Slug)
	}
	if nextSlug != current.Slug && !in.ConfirmSlugChange && !in.ConfirmModelIDChange {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "changing this Instance slug changes the OpenAI model ID; confirmation required"})
		return
	}
	runtime, _ := s.lifecycle.RuntimeInstance(r.Context(), current.ID)
	running := runtime.State != supervisor.Unloaded && runtime.State != supervisor.Failed
	if running && !in.RestartRunning {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "running Instance must restart to apply configuration; confirmation required"})
		return
	}
	if running {
		if err := s.lifecycle.StopInstance(r.Context(), current.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	item, err := s.lifecycle.Instances().Update(r.Context(), current.ID, in.UpdateInput)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if running {
		if _, err := s.lifecycle.StartInstance(r.Context(), item.ID); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "instance": item})
			return
		}
	}
	writeJSON(w, http.StatusOK, item)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, err)
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