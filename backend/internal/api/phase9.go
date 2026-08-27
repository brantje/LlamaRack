package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/recommendations"
)

type phase9RecommendationHandler struct {
	auth     *auth.Service
	models   *models.Service
	hardware hardware.Snapshotter
}

type phase9ModelInspectHandler struct {
	auth   *auth.Service
	models *models.Service
}

type phase9ModelDetailsHandler struct {
	auth   *auth.Service
	models *models.Service
}

func NewPhase9RecommendationHandler(a *auth.Service, modelService *models.Service, detector hardware.Snapshotter) http.Handler {
	return &phase9RecommendationHandler{auth: a, models: modelService, hardware: detector}
}

func NewPhase9ModelInspectHandler(a *auth.Service, modelService *models.Service) http.Handler {
	return &phase9ModelInspectHandler{auth: a, models: modelService}
}

func NewPhase9ModelDetailsHandler(a *auth.Service, modelService *models.Service) http.Handler {
	return &phase9ModelDetailsHandler{auth: a, models: modelService}
}

func (h *phase9RecommendationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !phase7RequireUser(h.auth, w, r) {
		return
	}
	id := modelIDFromRequest(r)
	model, err := h.models.GetByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "model not found"})
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if refreshed, refreshErr := h.models.RefreshLogicalSize(r.Context(), model.ID); refreshErr == nil {
		model = refreshed
	}
	contextLength := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("context_length")); raw != "" {
		contextLength, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || contextLength <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "context_length must be a positive integer"})
			return
		}
	}
	path, err := h.models.ModelAbsolutePath(model)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	snapshot, hardwareErr := h.hardware.Snapshot(r.Context())
	result := recommendations.Analyze(model, path, snapshot, contextLength, hardwareErr)
	writeJSON(w, http.StatusOK, result)
}

func (h *phase9ModelInspectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !phase7RequireUser(h.auth, w, r) {
		return
	}
	var in struct {
		GGUFPath string `json:"gguf_path"`
	}
	if !decode(w, r, &in) {
		return
	}
	inspection, err := h.models.InspectGGUF(in.GGUFPath)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"context_length": 0,
			"warning":        err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"architecture":   inspection.Derived.Architecture,
		"context_length": inspection.Derived.ContextLength,
		"gguf_version":   inspection.Version,
		"metadata_count": inspection.MetadataCount,
	})
}

func (h *phase9ModelDetailsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !phase7RequireUser(h.auth, w, r) {
		return
	}
	id := modelIDFromRequest(r)
	model, err := h.models.GetByID(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "model not found"})
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if refreshed, refreshErr := h.models.RefreshLogicalSize(r.Context(), model.ID); refreshErr == nil {
		model = refreshed
	}
	offset, limit, ok := metadataPage(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "offset and limit must be non-negative integers"})
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	inspection, inspectErr := h.models.InspectGGUF(model.GGUFPath)
	if inspectErr != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"model":          model,
			"metadata":       []ggufmeta.Entry{},
			"metadata_total": 0,
			"offset":         offset,
			"limit":          limit,
			"warnings":       []string{inspectErr.Error()},
		})
		return
	}
	warnings := append([]string(nil), inspection.Warnings...)
	if model.ContextLength <= 0 && inspection.Derived.ContextLength <= 0 {
		warnings = append(warnings, "Context capability could not be detected automatically from GGUF metadata.")
	}
	page, total := ggufmeta.Filter(inspection.Metadata, query, offset, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"model":                   model,
		"gguf_version":            inspection.Version,
		"tensor_count":            inspection.TensorCount,
		"metadata_count":          inspection.MetadataCount,
		"metadata_total":          total,
		"metadata":                page,
		"architecture":            inspection.Derived.Architecture,
		"detected_context_length": inspection.Derived.ContextLength,
		"offset":                  offset,
		"limit":                   limit,
		"warnings":                warnings,
	})
}

func modelIDFromRequest(r *http.Request) string {
	if id := strings.TrimSpace(r.PathValue("id")); id != "" {
		return id
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) >= 5 {
		return parts[3]
	}
	return ""
}

func metadataPage(r *http.Request) (int, int, bool) {
	offset := 0
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return 0, 0, false
		}
		offset = value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return 0, 0, false
		}
		limit = value
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	return offset, limit, true
}
