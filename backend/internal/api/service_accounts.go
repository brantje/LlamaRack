package api

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/brantje/llamarack/backend/internal/auth"
)

type serviceAccountsHandler struct{ auth *auth.Service }

func NewServiceAccountsHandler(a *auth.Service) http.Handler {
	return &serviceAccountsHandler{auth: a}
}

func (h *serviceAccountsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := managementAuthFromRequest(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/v1/admin/service-accounts" {
		h.collection(w, r, principal)
		return
	}
	id := strings.TrimPrefix(path, "/api/v1/admin/service-accounts/")
	if id == path || id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	h.item(w, r, principal, id)
}

func (h *serviceAccountsHandler) collection(w http.ResponseWriter, r *http.Request, principal managementAuthContext) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.auth.ListServiceAccounts(r.Context())
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
		item, err := h.auth.CreateServiceAccount(r.Context(), in.Name, principal.CreatedByUserID())
		if err != nil {
			writeServiceAccountMutationError(w, err)
			return
		}
		slog.Info("security event", append(actorLogAttrs(principal), "event", "service_account.created", "service_account_id", item.ID)...)
		writeJSON(w, http.StatusCreated, item)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (h *serviceAccountsHandler) item(w http.ResponseWriter, r *http.Request, principal managementAuthContext, id string) {
	switch r.Method {
	case http.MethodGet:
		item, err := h.auth.GetServiceAccount(r.Context(), id)
		if err != nil {
			writeServiceAccountError(w, err)
			return
		}
		keys := item.Keys
		if keys == nil {
			keys = []auth.APIKey{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":                 item.ID,
			"name":               item.Name,
			"enabled":            item.Enabled,
			"created_at":         item.CreatedAt,
			"created_by_user_id": item.CreatedByUserID,
			"keys":               keys,
		})
	case http.MethodPatch:
		var in struct {
			Name    *string `json:"name"`
			Enabled *bool   `json:"enabled"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := h.auth.UpdateServiceAccount(r.Context(), id, in.Name, in.Enabled); err != nil {
			writeServiceAccountMutationError(w, err)
			return
		}
		event := "service_account.updated"
		if in.Enabled != nil {
			event = "service_account.disabled"
			if *in.Enabled {
				event = "service_account.enabled"
			}
		}
		slog.Info("security event", append(actorLogAttrs(principal), "event", event, "service_account_id", id)...)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.auth.DeleteServiceAccount(r.Context(), id); err != nil {
			writeServiceAccountError(w, err)
			return
		}
		slog.Info("security event", append(actorLogAttrs(principal), "event", "service_account.deleted", "service_account_id", id)...)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeServiceAccountError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "service account not found"})
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

func writeServiceAccountMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "service account not found"})
	case errors.Is(err, auth.ErrServiceAccountNameRequired):
		writeErr(w, http.StatusBadRequest, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}
