package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/observability"
)

func TestResolveInstanceByIDUsesDurableIdentityAndHandlesFailures(t *testing.T) {
	f := newGatewayFixture(t, false)

	newObserved := func() (*responseObserver, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		return newResponseObserver(w, false), w
	}
	newRequest := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/v1/chat/completions/control", nil)
	}

	t.Run("resolves durable UUID without leaking it as the public slug", func(t *testing.T) {
		record := observability.RequestRecord{}
		observed, _ := newObserved()
		instance, ok := f.gateway.resolveInstanceByID(observed, newRequest(), &record, f.instanceID)
		if !ok {
			t.Fatal("expected durable ID lookup to succeed")
		}
		if instance.ID != f.instanceID || instance.Slug != "gateway-model" {
			t.Fatalf("instance=%+v durableID=%q", instance, f.instanceID)
		}
		if record.InstanceID != f.instanceID {
			t.Fatalf("record instance_id=%q want %q", record.InstanceID, f.instanceID)
		}
	})

	t.Run("authorizes against the durable UUID", func(t *testing.T) {
		req := newRequest()
		req = req.WithContext(context.WithValue(req.Context(), gatewayAllowlistKey{}, gatewayAllowlist{
			ids: map[string]struct{}{"00000000-0000-4000-8000-000000000001": {}},
		}))
		record := observability.RequestRecord{}
		observed, w := newObserved()
		if _, ok := f.gateway.resolveInstanceByID(observed, req, &record, f.instanceID); ok {
			t.Fatal("expected UUID allowlist denial")
		}
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "instance_not_allowed") {
			t.Fatalf("denied lookup=%d body=%s", w.Code, w.Body.String())
		}
		if record.InstanceID != f.instanceID {
			t.Fatalf("denied record instance_id=%q want %q", record.InstanceID, f.instanceID)
		}
	})

	t.Run("returns model not found for an unknown durable UUID", func(t *testing.T) {
		record := observability.RequestRecord{}
		observed, w := newObserved()
		if _, ok := f.gateway.resolveInstanceByID(observed, newRequest(), &record, "00000000-0000-4000-8000-ffffffffffff"); ok {
			t.Fatal("expected missing durable ID lookup to fail")
		}
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "model_not_found") {
			t.Fatalf("missing lookup=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("surfaces storage failures without treating them as missing models", func(t *testing.T) {
		if err := f.db.Close(); err != nil {
			t.Fatal(err)
		}
		record := observability.RequestRecord{}
		observed, w := newObserved()
		// Use an uncached durable ID: a warmed cache entry must remain usable even
		// after storage becomes unavailable, while a true cache miss must surface
		// the underlying storage failure.
		if _, ok := f.gateway.resolveInstanceByID(observed, newRequest(), &record, "00000000-0000-4000-8000-fffffffffff0"); ok {
			t.Fatal("expected closed database lookup to fail")
		}
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "model_unavailable") {
			t.Fatalf("storage failure=%d body=%s", w.Code, w.Body.String())
		}
		if record.Error == "" {
			t.Fatal("storage failure should be captured in request observability")
		}
	})
}
