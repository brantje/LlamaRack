package gateway

import (
	"context"
	"net/http"
	"testing"
)

func TestUnavailableModelRequestIDMapsToObservabilityRecord(t *testing.T) {
	f := newGatewayFixture(t, true)
	w := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{"model":"missing"}`)
	requestID := w.Header().Get(headerRequestID)
	if w.Code != http.StatusNotFound || requestID == "" {
		t.Fatalf("status=%d request_id=%q body=%s", w.Code, requestID, w.Body.String())
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), requestID)
	if err != nil {
		t.Fatal(err)
	}
	if record.InstanceID != "" || record.StatusCode != http.StatusNotFound || record.Result != "error" {
		t.Fatalf("correlated record=%+v", record)
	}
	var modelSlug string
	if err := f.db.QueryRowContext(context.Background(), `SELECT model_slug FROM inference_requests r JOIN inference_request_correlations c ON c.inference_request_id=r.id WHERE c.request_id=?`, requestID).Scan(&modelSlug); err != nil {
		t.Fatal(err)
	}
	if modelSlug != "missing" {
		t.Fatalf("model_slug=%q want missing", modelSlug)
	}
}
