package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/observability"
)

func TestActiveRegistryCancellationAuthorizationIsAtomic(t *testing.T) {
	registry := newActiveRegistry()
	cancelled := false
	registry.register(&activeRequest{
		managerRequestID: "lr_atomic",
		instanceID:       "instance-uuid",
		ownerKind:        observability.OwnerKindAPIKey,
		ownerID:          "key-a",
		upstreamID:       "resp_atomic",
		cancel:           func() { cancelled = true },
	})

	entry, didCancel, authResult := registry.cancelByUpstreamAuthorized("resp_atomic",
		func(ownerKind, ownerID string) bool { return ownerID == "key-a" },
		func(instanceID string) bool { return instanceID == "different-instance" },
	)
	if authResult != cancelAuthForbidden || didCancel || entry == nil || entry.instanceID != "instance-uuid" || cancelled {
		t.Fatalf("unauthorized cancellation mutated request: entry=%+v didCancel=%v authResult=%v cancelled=%v", entry, didCancel, authResult, cancelled)
	}

	entry, didCancel, authResult = registry.cancelByUpstreamAuthorized("resp_atomic",
		func(ownerKind, ownerID string) bool { return ownerID == "key-a" },
		func(instanceID string) bool { return instanceID == "instance-uuid" },
	)
	if authResult != cancelAuthOK || !didCancel || entry == nil || !entry.cancelled || !cancelled {
		t.Fatalf("authorized cancellation failed: entry=%+v didCancel=%v authResult=%v cancelled=%v", entry, didCancel, authResult, cancelled)
	}
}

func TestCaptureModelSlugBoundsPersistedMetadata(t *testing.T) {
	fixture := newGatewayFixture(t, false)
	ctx := context.Background()
	requestID := "lr_bounded_model_slug"
	fixture.gateway.begin(ctx, requestID, observability.RequestRecord{
		StartedAt: time.Now().UnixMilli(),
		Endpoint:  "/v1/chat/completions",
	})

	fixture.gateway.captureModelSlug(ctx, requestID, strings.Repeat("x", modelSlugCaptureLimit+128))
	identity, err := fixture.observability.RequestModelIdentity(ctx, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if len(identity.ModelSlug) != modelSlugCaptureLimit {
		t.Fatalf("captured model slug length=%d want=%d", len(identity.ModelSlug), modelSlugCaptureLimit)
	}
}

func TestGetModelAttributesObservabilityToDurableInstance(t *testing.T) {
	fixture := newGatewayFixture(t, false)
	response := gatewayRequest(t, fixture.gateway, http.MethodGet, "/v1/models/gateway-model", fixture.secret, "")
	if response.Code != http.StatusOK {
		t.Fatalf("get model=%d body=%s", response.Code, response.Body.String())
	}
	requestID := response.Header().Get(headerRequestID)
	identity, err := fixture.observability.RequestModelIdentity(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if identity.InstanceID != fixture.instanceID || identity.ModelSlug != "gateway-model" {
		t.Fatalf("model lookup identity=%+v durable=%q", identity, fixture.instanceID)
	}
}

func TestResolveInstanceStorageFailureReturnsGenericResponse(t *testing.T) {
	fixture := newGatewayFixture(t, false)
	if err := fixture.db.Close(); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	observed := newResponseObserver(recorder, false)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/models/gateway-model", nil)
	record := observability.RequestRecord{}
	if _, ok := fixture.gateway.resolveInstanceBySlug(observed, request, &record, "gateway-model"); ok {
		t.Fatal("storage failure unexpectedly resolved instance")
	}
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "The model is temporarily unavailable") {
		t.Fatalf("storage failure response=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(strings.ToLower(recorder.Body.String()), "database is closed") {
		t.Fatalf("storage detail leaked to client: %s", recorder.Body.String())
	}
	if record.Error == "" {
		t.Fatal("storage detail was not retained for observability")
	}
}
