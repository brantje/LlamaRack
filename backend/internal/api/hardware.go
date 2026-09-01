package api

import (
	"net/http"
	"strings"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/llamaconfig"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
)

type hardwareHandler struct {
	auth     *auth.Service
	hardware hardware.Snapshotter
}

func NewHardwareHandler(a *auth.Service, detector hardware.Snapshotter) http.Handler {
	return &hardwareHandler{auth: a, hardware: detector}
}

func (h *hardwareHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !requireAuthenticatedUser(h.auth, w, r) {
		return
	}
	snapshot, err := h.hardware.Snapshot(r.Context())
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

type llamaConfigHandler struct {
	auth    *auth.Service
	store   *llamaconfig.Store
	profile func() (llamacpp.Profile, error)
}

func NewLlamaConfigHandler(a *auth.Service, store *llamaconfig.Store, profile func() (llamacpp.Profile, error)) http.Handler {
	return &llamaConfigHandler{auth: a, store: store, profile: profile}
}

func (h *llamaConfigHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !requireAuthenticatedUser(h.auth, w, r) {
		return
	}
	profile, err := h.profile()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		modelID := strings.TrimSpace(r.URL.Query().Get("model_id"))
		instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
		effective, err := h.store.Effective(r.Context(), modelID, instanceID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		supported := make(map[string]bool, len(profile.Options))
		for _, option := range profile.Options {
			supported[option.Key] = true
		}
		unsupported := make([]string, 0)
		for key := range effective.Values {
			if !supported[key] {
				unsupported = append(unsupported, key)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"profile": profile, "effective": effective, "unsupported": unsupported})
	case http.MethodPut:
		if strings.TrimSpace(r.URL.Query().Get("model_id")) != "" || strings.TrimSpace(r.URL.Query().Get("instance_id")) != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this endpoint only writes global llama.cpp defaults"})
			return
		}
		var in struct {
			Options map[string]string `json:"options"`
		}
		if !decode(w, r, &in) {
			return
		}
		existing, err := h.store.Global(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		validated, err := llamacpp.ValidateOptionsRetaining(profile, in.Options, existing)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := h.store.ReplaceGlobal(r.Context(), validated); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		effective, err := h.store.Effective(r.Context(), "", "")
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"profile": profile, "effective": effective, "unsupported": []string{}})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func requireAuthenticatedUser(a *auth.Service, w http.ResponseWriter, r *http.Request) bool {
	if _, _, ok := managementAuthFromRequest(r); ok {
		return true
	}
	if token := bearerToken(r.Header.Get("Authorization")); token != "" {
		if _, _, err := a.AuthenticateBearer(r.Context(), token); err == nil {
			return true
		}
	}
	// Direct legacy unit fixtures bypass ManagementSecurity. Keep their cookie
	// path isolated here; production requests are bearer-gated by the middleware.
	if cookie := sessionCookieValue(r); cookie != "" {
		if _, _, err := a.SessionUserWithSession(r.Context(), cookie); err == nil {
			return true
		}
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	return false
}
