package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

type phase9ModelMetadataValueHandler struct {
	auth   *auth.Service
	models *models.Service
}

func NewPhase9ModelMetadataValueHandler(a *auth.Service, modelService *models.Service) http.Handler {
	return &phase9ModelMetadataValueHandler{auth: a, models: modelService}
}

func (h *phase9ModelMetadataValueHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !phase7RequireUser(h.auth, w, r) {
		return
	}
	model, err := h.models.GetByID(r.Context(), modelIDFromRequest(r))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "model not found"})
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key is required"})
		return
	}
	offset, limit, ok := metadataValuePage(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset and limit must be non-negative integers"})
		return
	}
	page, err := h.models.ReadGGUFValue(model.GGUFPath, key, offset, limit)
	if err != nil {
		if errors.Is(err, ggufmeta.ErrMetadataKeyNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "metadata key not found"})
			return
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func metadataValuePage(r *http.Request) (uint64, uint64, bool) {
	var offset, limit uint64
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		offset = value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		limit = value
	}
	return offset, limit, true
}
