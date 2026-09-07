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
		if !authorizeInFlightResponseAccess(w, r, inFlight) {
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
		writeResponseNotFound(w)
		return
	}
	if !authorizeStoredResponseAccess(w, r, stored) {
		return
	}
	payload, ok := parseFinalResponseJSON([]byte(*stored.ResponseBody))
	if !ok {
		writeResponseNotFound(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
	if !bytes.HasSuffix(payload, []byte("\n")) {
		_, _ = w.Write([]byte("\n"))
	}
}

func (g *Gateway) deleteStoredResponse(w http.ResponseWriter, r *http.Request, responseID string) {
	if g.observability == nil {
		writeResponseNotFound(w)
		return
	}
	stored, err := g.lookupStoredResponse(r.Context(), responseID)
	if err != nil || stored.Deleted {
		writeResponseNotFound(w)
		return
	}
	if !authorizeStoredResponseAccess(w, r, stored) {
		return
	}
	if err := g.observability.MarkOpenAIResponseDeleted(r.Context(), responseID); err != nil {
		writeResponseNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": responseID, "object": "response", "deleted": true})
}

func (g *Gateway) getResponseInputItems(w http.ResponseWriter, r *http.Request, responseID string) {
	stored, err := g.lookupStoredResponse(r.Context(), responseID)
	if err != nil || stored.Deleted || stored.RequestBody == nil || strings.TrimSpace(*stored.RequestBody) == "" {
		writeResponseNotFound(w)
		return
	}
	if !authorizeStoredResponseAccess(w, r, stored) {
		return
	}
	items := normalizeInputItems([]byte(*stored.RequestBody))
	writeJSON(w, http.StatusOK, inputItemsList(items, r.URL.Query().Get("after"), parseLimitQuery(r.URL.Query().Get("limit"))))
}

func (g *Gateway) cancelStoredResponse(w http.ResponseWriter, r *http.Request, responseID string) {
	responseID = strings.TrimSpace(responseID)
	entry, cancelled, authResult := g.active.cancelByUpstreamAuthorized(responseID,
		func(ownerKind, ownerID string) bool { return responseOwnerMatches(r, ownerKind, ownerID) },
		func(instanceID string) bool { return requestInstanceAllowed(r, instanceID) },
	)
	switch authResult {
	case cancelAuthNotFound:
		if entry == nil {
			break
		}
		writeResponseNotFound(w)
		return
	case cancelAuthForbidden:
		writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
		return
	}
	if cancelled {
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
	if entry != nil && entry.cancelled {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "Response is already cancelled")
		return
	}
	stored, err := g.lookupStoredResponse(r.Context(), responseID)
	if err != nil || stored.Deleted {
		writeResponseNotFound(w)
		return
	}
	if !authorizeStoredResponseAccess(w, r, stored) {
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "Response is not cancellable")
}

func (g *Gateway) lookupStoredResponse(ctx context.Context, responseID string) (observability.StoredOpenAIResponse, error) {
	if g.observability == nil {
		return observability.StoredOpenAIResponse{}, errors.New("observability unavailable")
	}
	return g.observability.GetStoredOpenAIResponse(ctx, responseID)
}
