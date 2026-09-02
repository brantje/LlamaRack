package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/brantje/llamarack/backend/internal/auth"
)

type apiKeysHandler struct{ auth *auth.Service }

func NewAPIKeysHandler(a *auth.Service) http.Handler { return &apiKeysHandler{auth: a} }

func (h *apiKeysHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := managementAuthFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/v1/api-keys" {
		h.collection(w, r, principal)
		return
	}
	rest := strings.TrimPrefix(path, "/api/v1/api-keys/")
	if rest == path || rest == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	h.item(w, r, principal, rest)
}

func (h *apiKeysHandler) collection(w http.ResponseWriter, r *http.Request, principal managementAuthContext) {
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
			Name                  string   `json:"name"`
			KeyType               string   `json:"key_type"`
			OwnerUserID           *int64   `json:"owner_user_id"`
			OwnerServiceAccountID *string  `json:"owner_service_account_id"`
			InstanceIDs           []string `json:"instance_ids"`
			ExpiresOn             *string  `json:"expires_on"`
		}
		if !decode(w, r, &in) {
			return
		}
		expiresOn := ""
		if in.ExpiresOn != nil {
			expiresOn = *in.ExpiresOn
		}
		saID := ""
		if in.OwnerServiceAccountID != nil {
			saID = *in.OwnerServiceAccountID
		}
		var creator *int64
		if principal.User != nil {
			id := principal.User.ID
			creator = &id
		}
		key, secret, err := h.auth.CreateAPIKey(r.Context(), auth.CreateAPIKeyInput{
			Name:                  in.Name,
			KeyType:               in.KeyType,
			OwnerUserID:           in.OwnerUserID,
			OwnerServiceAccountID: saID,
			InstanceIDs:           in.InstanceIDs,
			ExpiresOn:             expiresOn,
			CreatedByUserID:       creator,
		})
		if err != nil {
			writeAPIKeyMutationError(w, err)
			return
		}
		slog.Info("security event", append(actorLogAttrs(principal), "event", "api_key.created", "key_id", key.ID)...)
		writeJSON(w, http.StatusCreated, map[string]any{"key": key, "secret": secret})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *apiKeysHandler) item(w http.ResponseWriter, r *http.Request, principal managementAuthContext, rest string) {
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "api key not found"})
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.patch(w, r, principal, id)
		return
	}
	if len(parts) != 2 || parts[1] != "rotate" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	key, secret, err := h.auth.RotateAPIKey(r.Context(), id)
	if err != nil {
		writeAPIKeyError(w, err)
		return
	}
	slog.Info("security event", append(actorLogAttrs(principal), "event", "api_key.rotated", "key_id", id)...)
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "secret": secret})
}

func (h *apiKeysHandler) patch(w http.ResponseWriter, r *http.Request, principal managementAuthContext, id string) {
	var in struct {
		Name                  *string          `json:"name"`
		OwnerUserID           *int64           `json:"owner_user_id"`
		OwnerServiceAccountID *string          `json:"owner_service_account_id"`
		InstanceIDs           *[]string        `json:"instance_ids"`
		ExpiresOn             json.RawMessage `json:"expires_on"`
		Enabled               *bool           `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	update := auth.UpdateAPIKeyInput{
		Name:                  in.Name,
		OwnerUserID:           in.OwnerUserID,
		OwnerServiceAccountID: in.OwnerServiceAccountID,
		InstanceIDs:           in.InstanceIDs,
		Enabled:               in.Enabled,
	}
	if len(in.ExpiresOn) > 0 {
		raw := strings.TrimSpace(string(in.ExpiresOn))
		if raw == "null" {
			update.ClearExpiresOn = true
		} else {
			var expires string
			if err := json.Unmarshal(in.ExpiresOn, &expires); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": auth.ErrAPIKeyExpiresOnInvalid.Error()})
				return
			}
			update.ExpiresOn = &expires
		}
	}
	if err := h.auth.UpdateAPIKey(r.Context(), id, update); err != nil {
		writeAPIKeyMutationError(w, err)
		return
	}
	event := "api_key.updated"
	if in.Enabled != nil {
		event = "api_key.disabled"
		if *in.Enabled {
			event = "api_key.enabled"
		}
	}
	slog.Info("security event", append(actorLogAttrs(principal), "event", event, "key_id", id)...)
	w.WriteHeader(http.StatusNoContent)
}

func writeAPIKeyError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "api key not found"})
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

func writeAPIKeyMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "api key not found"})
	case errors.Is(err, auth.ErrAPIKeyNameRequired),
		errors.Is(err, auth.ErrAPIKeyTypeInvalid),
		errors.Is(err, auth.ErrAPIKeyOwnerRequired),
		errors.Is(err, auth.ErrAPIKeyOwnerDisabled),
		errors.Is(err, auth.ErrAPIKeyOwnerNotFound),
		errors.Is(err, auth.ErrAPIKeyInstancesNotAllowed),
		errors.Is(err, auth.ErrUnknownInstanceID),
		errors.Is(err, auth.ErrAPIKeyExpiresOnInvalid),
		errors.Is(err, auth.ErrAPIKeyExpiresOnPast):
		writeErr(w, http.StatusBadRequest, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}
