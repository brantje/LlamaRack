package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/observability"
)

func TestResolveInstanceBySlugHandlesMissingAndStorageFailures(t *testing.T) {
	t.Run("missing public slug", func(t *testing.T) {
		f := newGatewayFixture(t, false)
		w := httptest.NewRecorder()
		observed := newResponseObserver(w, false)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		record := observability.RequestRecord{}
		if _, ok := f.gateway.resolveInstanceBySlug(observed, req, &record, "missing-public-slug"); ok {
			t.Fatal("expected missing public slug lookup to fail")
		}
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "model_not_found") {
			t.Fatalf("missing slug=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("storage failure", func(t *testing.T) {
		f := newGatewayFixture(t, false)
		if err := f.db.Close(); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		observed := newResponseObserver(w, false)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		record := observability.RequestRecord{}
		if _, ok := f.gateway.resolveInstanceBySlug(observed, req, &record, "gateway-model"); ok {
			t.Fatal("expected closed database slug lookup to fail")
		}
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "model_unavailable") {
			t.Fatalf("storage failure=%d body=%s", w.Code, w.Body.String())
		}
		if record.Error == "" {
			t.Fatal("storage failure should be recorded")
		}
	})
}
