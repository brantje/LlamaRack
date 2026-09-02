package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/litellm"
)

type liteLLMHandler struct {
	auth *auth.Service
	svc  *litellm.Service
}

func NewLiteLLMHandler(a *auth.Service, svc *litellm.Service) http.Handler {
	return &liteLLMHandler{auth: a, svc: svc}
}

func (h *liteLLMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !requireAuthenticatedUser(h.auth, w, r) {
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/v1/litellm" && r.Method == http.MethodGet:
		h.status(w, r)
	case path == "/api/v1/litellm" && r.Method == http.MethodPut:
		h.save(w, r)
	case path == "/api/v1/litellm" && r.Method == http.MethodDelete:
		h.disconnect(w, r)
	case path == "/api/v1/litellm/test" && r.Method == http.MethodPost:
		h.test(w, r)
	case path == "/api/v1/litellm/sync" && r.Method == http.MethodPost:
		h.sync(w, r)
	case path == "/api/v1/litellm/rotate" && r.Method == http.MethodPost:
		h.rotate(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *liteLLMHandler) status(w http.ResponseWriter, r *http.Request) {
	status, err := h.svc.Status(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *liteLLMHandler) save(w http.ResponseWriter, r *http.Request) {
	var in litellm.SaveInput
	if !decode(w, r, &in) {
		return
	}
	status, err := h.svc.Save(r.Context(), in)
	if err != nil {
		writeLiteLLMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *liteLLMHandler) test(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Test(r.Context()); err != nil {
		writeLiteLLMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *liteLLMHandler) sync(w http.ResponseWriter, r *http.Request) {
	result, err := h.svc.Reconcile(r.Context())
	if err != nil {
		writeLiteLLMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *liteLLMHandler) rotate(w http.ResponseWriter, r *http.Request) {
	status, err := h.svc.Rotate(r.Context())
	if err != nil {
		writeLiteLLMError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *liteLLMHandler) disconnect(w http.ResponseWriter, r *http.Request) {
	var in litellm.DisconnectInput
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &in) {
			return
		}
	}
	if err := h.svc.Disconnect(r.Context(), in); err != nil {
		writeLiteLLMError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeLiteLLMError(w http.ResponseWriter, err error) {
	if errors.Is(err, litellm.ErrStoreModelInDB) {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "required"),
		strings.Contains(message, "not configured"),
		strings.Contains(message, "invalid proxy url"),
		strings.Contains(message, "must use http"):
		writeErr(w, http.StatusBadRequest, err)
	default:
		writeErr(w, http.StatusBadGateway, err)
	}
}
