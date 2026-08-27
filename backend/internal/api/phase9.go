package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/recommendations"
)

type phase9RecommendationHandler struct {
	auth     *auth.Service
	models   *models.Service
	hardware hardware.Snapshotter
}

func NewPhase9RecommendationHandler(a *auth.Service, modelService *models.Service, detector hardware.Snapshotter) http.Handler {
	return &phase9RecommendationHandler{auth: a, models: modelService, hardware: detector}
}

func (h *phase9RecommendationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !phase7RequireUser(h.auth, w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		// Keep the handler easy to unit test without a ServeMux path pattern.
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 5 { id = parts[3] }
	}
	model, err := h.models.GetByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows { writeJSON(w, http.StatusNotFound, map[string]string{"error":"model not found"}); return }
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	contextLength := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("context_length")); raw != "" {
		contextLength, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || contextLength <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error":"context_length must be a positive integer"})
			return
		}
	}
	path, err := h.models.ModelAbsolutePath(model)
	if err != nil { writeErr(w, http.StatusInternalServerError, err); return }
	snapshot, hardwareErr := h.hardware.Snapshot(r.Context())
	result := recommendations.Analyze(model, path, snapshot, contextLength, hardwareErr)
	writeJSON(w, http.StatusOK, result)
}
