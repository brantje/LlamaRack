package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/recommendations"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

type discoverRecommendationHandler struct {
	auth     *auth.Service
	hf       *huggingface.Client
	hardware hardware.Snapshotter
	settings *settings.Service
}

type discoverSettingsHandler struct {
	auth     *auth.Service
	settings *settings.Service
}

func NewDiscoverRecommendationHandler(a *auth.Service, hf *huggingface.Client, detector hardware.Snapshotter, managerSettings *settings.Service) http.Handler {
	return &discoverRecommendationHandler{auth: a, hf: hf, hardware: detector, settings: managerSettings}
}

func NewDiscoverSettingsHandler(a *auth.Service, managerSettings *settings.Service) http.Handler {
	return &discoverSettingsHandler{auth: a, settings: managerSettings}
}

func (h *discoverRecommendationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !requireAuthenticatedUser(h.auth, w, r) {
		return
	}
	repoID := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repoID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo is required"})
		return
	}
	contextLength := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("context_length")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "context_length must be a positive integer"})
			return
		}
		contextLength = value
	}
	assumeIdle := true
	if raw := strings.TrimSpace(r.URL.Query().Get("assume_idle")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "assume_idle must be a boolean"})
			return
		}
		assumeIdle = value
	}

	detail, err := h.hf.Detail(r.Context(), repoID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	derived, metadataErr := h.hf.DerivedMetadata(r.Context(), detail)
	snapshot, hardwareErr := h.hardware.Snapshot(r.Context())
	policy, policyErr := h.settings.Discover(r.Context())
	if policyErr != nil {
		writeErr(w, http.StatusInternalServerError, policyErr)
		return
	}
	allowHybrid, _ := policy.HybridRecommendations.Value.(bool)
	inputs := make([]recommendations.ArtifactInput, 0, len(detail.Artifacts))
	for _, artifact := range detail.Artifacts {
		inputs = append(inputs, recommendations.ArtifactInput{
			ID: artifact.ID, Quantization: artifact.ProfileQuantization(), WeightsBytes: artifact.ModelBytes, Complete: artifact.Complete,
		})
	}
	result := recommendations.AnalyzeDiscover(inputs, recommendationMetadata(derived), metadataErr, snapshot, contextLength, hardwareErr, allowHybrid, assumeIdle)
	writeJSON(w, http.StatusOK, result)
}

func recommendationMetadata(value ggufmeta.Derived) recommendations.Metadata {
	return recommendations.Metadata{
		Architecture: value.Architecture,
		ContextLength: value.ContextLength,
		BlockCount: value.BlockCount,
		Embedding: value.Embedding,
		HeadCount: value.HeadCount,
		KVHeadCount: value.KVHeadCount,
		KeyLength: value.KeyLength,
		ValueLength: value.ValueLength,
	}
}

func (h *discoverSettingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !requireAuthenticatedUser(h.auth, w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		value, err := h.settings.Discover(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	case http.MethodPut:
		var in struct {
			HybridRecommendations *bool `json:"hybrid_recommendations_enabled"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.HybridRecommendations == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hybrid_recommendations_enabled is required"})
			return
		}
		value, err := h.settings.SetDiscoverHybridRecommendations(r.Context(), *in.HybridRecommendations)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, value)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
