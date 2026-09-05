package gateway

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/brantje/llamarack/backend/internal/observability"
)

func (g *Gateway) getStoredResponse(w http.ResponseWriter, r *http.Request, responseID string) {
	responseID = strings.TrimSpace(responseID)
	if inFlight := g.active.getByUpstream(responseID); inFlight != nil && strings.HasPrefix(inFlight.endpoint, "/v1/responses") {
		if !requestInstanceAllowed(r, inFlight.instanceID) {
			writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id": responseID, "object": "response", "status": "in_progress",
			"model": inFlight.model, "created_at": inFlight.startedAt / 1000,
		})
		return
	}
	stored, err := g.lookupStoredResponse(r.Context(), responseID)
	if err != nil || stored.Deleted || stored.ResponseBody == nil || strings.TrimSpace(*stored.ResponseBody) == "" {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Response not found")
		return
	}
	if !requestInstanceAllowed(r, stored.InstanceID) {
		writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
		return
	}
	payload, ok := parseFinalResponseJSON([]byte(*stored.ResponseBody))
	if !ok {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Response not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
	if !bytes.HasSuffix(payload, []byte("\n")) { _, _ = w.Write([]byte("\n")) }
}

func (g *Gateway) deleteStoredResponse(w http.ResponseWriter, r *http.Request, responseID string) {
	if g.observability == nil {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Response not found")
		return
	}
	stored, err := g.lookupStoredResponse(r.Context(), responseID)
	if err != nil || stored.Deleted {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Response not found")
		return
	}
	if !requestInstanceAllowed(r, stored.InstanceID) {
		writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
		return
	}
	if err := g.observability.MarkOpenAIResponseDeleted(r.Context(), responseID); err != nil {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Response not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": responseID, "object": "response", "deleted": true})
}

func (g *Gateway) getResponseInputItems(w http.ResponseWriter, r *http.Request, responseID string) {
	stored, err := g.lookupStoredResponse(r.Context(), responseID)
	if err != nil || stored.Deleted || stored.RequestBody == nil || strings.TrimSpace(*stored.RequestBody) == "" {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Response not found")
		return
	}
	if !requestInstanceAllowed(r, stored.InstanceID) {
		writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
		return
	}
	items := normalizeInputItems([]byte(*stored.RequestBody))
	writeJSON(w, http.StatusOK, inputItemsList(items, r.URL.Query().Get("after"), parseLimitQuery(r.URL.Query().Get("limit"))))
}

func (g *Gateway) cancelStoredResponse(w http.ResponseWriter, r *http.Request, responseID string) {
	responseID = strings.TrimSpace(responseID)
	if preview := g.active.getByUpstream(responseID); preview != nil && !requestInstanceAllowed(r, preview.instanceID) {
		writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
		return
	}
	if entry, ok := g.active.cancelByUpstream(responseID); ok {
		_ = g.active.waitRemoved(entry.managerRequestID, 2*time.Second)
		if stored, err := g.lookupStoredResponse(r.Context(), responseID); err == nil && stored.ResponseBody != nil {
			if payload, parsed := parseFinalResponseJSON([]byte(*stored.ResponseBody)); parsed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(payload)
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": responseID, "object": "response", "status": "cancelled", "model": entry.model})
		return
	}
	if entry := g.active.getByUpstream(responseID); entry != nil && entry.cancelled {
		if !requestInstanceAllowed(r, entry.instanceID) {
			writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "Response is already cancelled")
		return
	}
	stored, err := g.lookupStoredResponse(r.Context(), responseID)
	if err != nil || stored.Deleted {
		writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Response not found")
		return
	}
	if !requestInstanceAllowed(r, stored.InstanceID) {
		writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "Response is not cancellable")
}

func (g *Gateway) lookupStoredResponse(ctx context.Context, responseID string) (observability.StoredOpenAIResponse, error) {
	if g.observability == nil { return observability.StoredOpenAIResponse{}, errors.New("observability unavailable") }
	return g.observability.GetStoredOpenAIResponse(ctx, responseID)
}
