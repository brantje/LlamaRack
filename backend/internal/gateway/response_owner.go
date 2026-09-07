package gateway

import (
	"context"
	"net/http"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/observability"
)

type gatewayResponseOwnerKey struct{}

type gatewayResponseOwner struct {
	kind string
	id   string
}

func stampResponseOwner(r *http.Request, record *observability.RequestRecord, key auth.APIKey) *http.Request {
	owner := gatewayResponseOwner{}
	if principal, ok := auth.TrustedInferencePrincipalFromContext(r.Context()); ok {
		owner.kind = principal.Kind
		owner.id = principal.ID
	} else if key.ID != "" {
		owner.kind = observability.OwnerKindAPIKey
		owner.id = key.ID
	}
	record.OwnerKind = owner.kind
	record.OwnerID = owner.id
	return r.WithContext(withResponseOwner(r.Context(), owner))
}

func withResponseOwner(ctx context.Context, owner gatewayResponseOwner) context.Context {
	return context.WithValue(ctx, gatewayResponseOwnerKey{}, owner)
}

func requestResponseOwner(r *http.Request) (kind, id string) {
	if owner, ok := r.Context().Value(gatewayResponseOwnerKey{}).(gatewayResponseOwner); ok {
		return owner.kind, owner.id
	}
	return "", ""
}

func responseOwnerMatches(r *http.Request, ownerKind, ownerID string) bool {
	callerKind, callerID := requestResponseOwner(r)
	return callerKind != "" && callerID != "" && callerKind == ownerKind && callerID == ownerID
}

func writeResponseNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "invalid_request_error", "not_found", "Response not found")
}

func authorizeStoredResponseAccess(w http.ResponseWriter, r *http.Request, stored observability.StoredOpenAIResponse) bool {
	if !responseOwnerMatches(r, stored.OwnerKind, stored.OwnerID) {
		writeResponseNotFound(w)
		return false
	}
	if !requestInstanceAllowed(r, stored.InstanceID) {
		writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
		return false
	}
	return true
}

func authorizeInFlightResponseAccess(w http.ResponseWriter, r *http.Request, inFlight *activeRequest) bool {
	if inFlight == nil {
		return false
	}
	if !responseOwnerMatches(r, inFlight.ownerKind, inFlight.ownerID) {
		writeResponseNotFound(w)
		return false
	}
	if !requestInstanceAllowed(r, inFlight.instanceID) {
		writeError(w, http.StatusForbidden, "permission_error", "instance_not_allowed", "This API key cannot access that instance")
		return false
	}
	return true
}
