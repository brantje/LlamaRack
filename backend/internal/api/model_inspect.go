package api

import (
	"errors"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/ggufmeta"
	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamaconfig"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/recommendations"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

type recommendationHandler struct {
	auth      *auth.Service
	models    *models.Service
	instances *instances.Service
	config    *llamaconfig.Store
	hardware  hardware.Snapshotter
	profile      func() (llamacpp.Profile, error)
	reservations *scheduler.Ledger
}

type modelInspectHandler struct {
	auth   *auth.Service
	models *models.Service
}

type modelDetailsHandler struct {
	auth   *auth.Service
	models *models.Service
}

func NewRecommendationHandler(a *auth.Service, modelService *models.Service, instanceService *instances.Service, configStore *llamaconfig.Store, detector hardware.Snapshotter, profileGetters ...func() (llamacpp.Profile, error)) http.Handler {
	return newRecommendationHandler(a, modelService, instanceService, configStore, detector, nil, profileGetters...)
}

func NewReservationAwareRecommendationHandler(a *auth.Service, modelService *models.Service, instanceService *instances.Service, configStore *llamaconfig.Store, detector hardware.Snapshotter, reservations *scheduler.Ledger, profileGetters ...func() (llamacpp.Profile, error)) http.Handler {
	return newRecommendationHandler(a, modelService, instanceService, configStore, detector, reservations, profileGetters...)
}

func newRecommendationHandler(a *auth.Service, modelService *models.Service, instanceService *instances.Service, configStore *llamaconfig.Store, detector hardware.Snapshotter, reservations *scheduler.Ledger, profileGetters ...func() (llamacpp.Profile, error)) http.Handler {
	var profile func() (llamacpp.Profile, error)
	if len(profileGetters) > 0 {
		profile = profileGetters[0]
	}
	return &recommendationHandler{auth: a, models: modelService, instances: instanceService, config: configStore, hardware: detector, profile: profile, reservations: reservations}
}

func NewModelInspectHandler(a *auth.Service, modelService *models.Service) http.Handler {
	return &modelInspectHandler{auth: a, models: modelService}
}

func NewModelDetailsHandler(a *auth.Service, modelService *models.Service) http.Handler {
	return &modelDetailsHandler{auth: a, models: modelService}
}

func (h *recommendationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !requireAuthenticatedUser(h.auth, w, r) {
		return
	}
	id := modelIDFromRequest(r)
	model, err := h.models.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
	runtime := recommendations.RuntimeConfig{GPUMode: "auto"}
	instanceID := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if instanceID != "" {
		instance, instanceErr := h.instances.Get(r.Context(), instanceID)
		if instanceErr != nil {
			if errors.Is(instanceErr, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "instance not found"})
				return
			}
			writeErr(w, http.StatusInternalServerError, instanceErr)
			return
		}
		if instance.ModelID != model.ID {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "instance does not belong to model"})
			return
		}
		runtime.GPUMode = instance.GPUMode
		runtime.GPUDevices = append([]string(nil), instance.GPUDevices...)
		runtime.TensorSplit = instance.TensorSplit
		runtime.AllowSystemSpillover = instance.SystemSpilloverEnabled
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("gpu_mode")); raw != "" {
		if raw != "auto" && raw != "manual" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "gpu_mode must be auto or manual"})
			return
		}
		runtime.GPUMode = raw
	}
	if raw, ok := r.URL.Query()["gpu_devices"]; ok {
		runtime.GPUDevices = splitRecommendationDevices(strings.Join(raw, ","))
	}
	if raw, ok := r.URL.Query()["tensor_split"]; ok {
		runtime.TensorSplit = strings.TrimSpace(strings.Join(raw, ","))
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("system_spillover_enabled")); raw != "" {
		enabled, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "system_spillover_enabled must be true or false"})
			return
		}
		runtime.AllowSystemSpillover = enabled
	}

	previewOptions, previewOptionsPresent, previewErr := recommendationPreviewOptions(r)
	if previewErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": previewErr.Error()})
		return
	}

	store := h.config
	effective, configErr := store.Effective(r.Context(), model.ID, instanceID)
	if configErr != nil {
		writeErr(w, http.StatusInternalServerError, configErr)
		return
	}
	runtime.Options = effective.Values
	capabilities := recommendations.Capabilities{}
	if h.profile != nil {
		if profile, profileErr := h.profile(); profileErr == nil {
			capabilities = recommendationCapabilitiesFromProfile(profile)
			var launchOptions map[string]string
			var launchErr error
			if previewOptionsPresent {
				launchOptions, _, launchErr = store.PreviewLaunchOptions(r.Context(), profile, model.ID, instanceID, previewOptions)
			} else {
				launchOptions, _, launchErr = store.LaunchOptions(r.Context(), profile, model.ID, instanceID)
			}
			if launchErr != nil {
				writeErr(w, http.StatusBadRequest, launchErr)
				return
			}
			runtime.Options = launchOptions
		} else if previewOptionsPresent {
			writeErr(w, http.StatusBadRequest, profileErr)
			return
		}
	} else if previewOptionsPresent {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "llama-server option schema is unavailable"})
		return
	}
	runtime.CompanionBytes = recommendations.CompanionBytes(runtime.Options)
	if hardwareErr == nil && h.reservations != nil {
		owner := scheduler.ResourceOwner{}
		if instanceID != "" {
			owner = scheduler.ResourceOwner{Kind: scheduler.ResourceOwnerInstance, ID: instanceID}
		}
		snapshot = h.reservations.PlanningSnapshot(snapshot, owner)
	}
	result := recommendations.AnalyzeRuntime(model, path, snapshot, contextLength, hardwareErr, capabilities, runtime)
	writeJSON(w, http.StatusOK, result)
}

func (h *modelInspectHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !requireAuthenticatedUser(h.auth, w, r) {
		return
	}
	var in struct {
		GGUFPath string `json:"gguf_path"`
	}
	if !decode(w, r, &in) {
		return
	}
	inspection, err := h.models.InspectGGUFArtifact(r.Context(), in.GGUFPath)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"context_length": 0,
			"warning":        err.Error(),
		})
		return
	}
	candidates, candidateErr := h.models.InspectGGUFArtifactCandidates(r.Context(), in.GGUFPath)
	if candidateErr != nil {
		candidates = nil
	}
	writeJSON(w, http.StatusOK, struct {
		models.GGUFInspection
		DependencyCandidates []models.GGUFArtifactDependencyCandidate `json:"dependency_candidates,omitempty"`
	}{GGUFInspection: inspection, DependencyCandidates: candidates})
}

func (h *modelDetailsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !requireAuthenticatedUser(h.auth, w, r) {
		return
	}
	id := modelIDFromRequest(r)
	model, err := h.models.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
	payload := map[string]any{
		"model":                   model,
		"gguf_version":            inspection.Version,
		"tensor_count":            inspection.TensorCount,
		"metadata_count":          inspection.MetadataCount,
		"metadata_total":          total,
		"metadata":                page,
		"architecture":            inspection.Derived.Architecture,
		"detected_context_length": inspection.Derived.ContextLength,
		"features":                inspection.Features,
		"offset":                  offset,
		"limit":                   limit,
		"warnings":                warnings,
	}
	writeJSON(w, http.StatusOK, payload)
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

func splitRecommendationDevices(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}


func recommendationPreviewOptions(r *http.Request) (map[string]string, bool, error) {
	values, ok := r.URL.Query()["preview_options"]
	if !ok {
		return nil, false, nil
	}
	raw := "{}"
	if len(values) > 0 && strings.TrimSpace(values[0]) != "" {
		raw = values[0]
	}
	options := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return nil, true, fmt.Errorf("preview_options must be a JSON object of llama.cpp option strings: %w", err)
	}
	return options, true, nil
}
