package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/brantje/llamarack/backend/internal/auth"
)

type apiKeysHandler struct{ auth *auth.Service }

func NewAPIKeysHandler(a *auth.Service) http.Handler { return &apiKeysHandler{auth: a} }

func (h *apiKeysHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, _, ok := managementAuthFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/v1/api-keys" {
		h.collection(w, r, user)
		return
	}
	rest := strings.TrimPrefix(path, "/api/v1/api-keys/")
	if rest == path || rest == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	h.item(w, r, user, rest)
}

func (h *apiKeysHandler) collection(w http.ResponseWriter, r *http.Request, user auth.User) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.auth.ListAPIKeys(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case http.MethodPost:
		var in struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &in) {
			return
		}
		key, secret, err := h.auth.CreateAPIKeyForUser(r.Context(), in.Name, user.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		slog.Info("security event", "event", "api_key.created", "actor_user_id", user.ID, "key_id", key.ID)
		writeJSON(w, http.StatusCreated, map[string]any{"key": key, "secret": secret})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *apiKeysHandler) item(w http.ResponseWriter, r *http.Request, user auth.User, rest string) {
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "api key not found"})
		return
	}
	if len(parts) == 1 {
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
			if err := h.auth.SetAPIKeyEnabled(r.Context(), id, *in.Enabled); err != nil {
				writeAPIKeyError(w, err)
				return
			}
			event := "api_key.disabled"
			if *in.Enabled {
				event = "api_key.enabled"
			}
			slog.Info("security event", "event", event, "actor_user_id", user.ID, "key_id", id)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			h.revoke(w, r, user, id)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) != 2 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	switch parts[1] {
	case "revoke":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.revoke(w, r, user, id)
	case "rotate":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		key, secret, err := h.auth.RotateAPIKey(r.Context(), id, user.ID)
		if err != nil {
			writeAPIKeyError(w, err)
			return
		}
		slog.Info("security event", "event", "api_key.rotated", "actor_user_id", user.ID, "old_key_id", id, "new_key_id", key.ID)
		writeJSON(w, http.StatusCreated, map[string]any{"key": key, "secret": secret})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *apiKeysHandler) revoke(w http.ResponseWriter, r *http.Request, user auth.User, id string) {
	if err := h.auth.RevokeAPIKey(r.Context(), id); err != nil {
		writeAPIKeyError(w, err)
		return
	}
	slog.Info("security event", "event", "api_key.revoked", "actor_user_id", user.ID, "key_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func writeAPIKeyError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "api key not found"})
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}
