package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/observability"
)

func fixtureOwnedResponseRecord(f *gatewayFixture, startedAt, finishedAt int64, requestBody, responseBody *string) observability.RequestRecord {
	return observability.RequestRecord{
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
		InstanceID:   f.instanceID,
		Endpoint:     "/v1/responses",
		StatusCode:   http.StatusOK,
		Result:       "success",
		OwnerKind:    observability.OwnerKindAPIKey,
		OwnerID:      f.keyID,
		APIKey:       &observability.APIKeyRef{ID: f.keyID, Name: "gateway"},
		RequestBody:  requestBody,
		ResponseBody: responseBody,
	}
}

func seedStoredResponse(t *testing.T, f *gatewayFixture, requestID, responseID string, requestBody, responseBody string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UnixMilli()
	reqBody := requestBody
	respBody := responseBody
	if err := f.observability.RecordCorrelatedRequest(ctx, requestID, nil, fixtureOwnedResponseRecord(f, now, now, &reqBody, &respBody)); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.SetOpenAIResponseID(ctx, requestID, responseID); err != nil {
		t.Fatal(err)
	}
}

func TestResponseOwnerStoredAccess(t *testing.T) {
	f := newGatewayFixture(t, false)
	requestBody := `{"model":"gateway-model","input":"hello"}`
	responseBody := `{"id":"resp_owned","object":"response","status":"completed","output":[{"type":"message"}]}`
	seedStoredResponse(t, f, "lr_owned", "resp_owned", requestBody, responseBody)

	got := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/resp_owned", f.secret, "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"resp_owned"`) {
		t.Fatalf("owner retrieve=%d %s", got.Code, got.Body.String())
	}
	items := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/resp_owned/input_items", f.secret, "")
	if items.Code != http.StatusOK || !strings.Contains(items.Body.String(), `"hello"`) {
		t.Fatalf("owner input items=%d %s", items.Code, items.Body.String())
	}
	deleted := gatewayRequest(t, f.gateway, http.MethodDelete, "/v1/responses/resp_owned", f.secret, "")
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"deleted":true`) {
		t.Fatalf("owner delete=%d %s", deleted.Code, deleted.Body.String())
	}
}

func TestResponseOwnerCrossKeyDenied(t *testing.T) {
	f := newGatewayFixture(t, false)
	ctx := context.Background()
	_, otherSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "other", KeyType: auth.APIKeyTypeInference, OwnerUserID: &f.ownerID, InstanceIDs: []string{f.instanceID},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, fullSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "full", KeyType: auth.APIKeyTypeFull, OwnerUserID: &f.ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}

	requestBody := `{"model":"gateway-model","input":"secret"}`
	responseBody := `{"id":"resp_cross","object":"response","status":"completed"}`
	seedStoredResponse(t, f, "lr_cross", "resp_cross", requestBody, responseBody)

	missing := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/missing", f.secret, "")
	crossGet := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/resp_cross", otherSecret, "")
	if crossGet.Code != http.StatusNotFound || crossGet.Body.String() != missing.Body.String() {
		t.Fatalf("cross-key get=%d body=%q want=%q", crossGet.Code, crossGet.Body.String(), missing.Body.String())
	}
	for _, path := range []string{
		"/v1/responses/resp_cross/input_items",
		"/v1/responses/resp_cross",
	} {
		method := http.MethodGet
		if path == "/v1/responses/resp_cross" && strings.HasSuffix(path, "resp_cross") {
			// delete tested separately
		}
		w := gatewayRequest(t, f.gateway, method, path, otherSecret, "")
		if w.Code != http.StatusNotFound {
			t.Fatalf("cross-key %s %s=%d", method, path, w.Code)
		}
	}
	if w := gatewayRequest(t, f.gateway, http.MethodDelete, "/v1/responses/resp_cross", otherSecret, ""); w.Code != http.StatusNotFound {
		t.Fatalf("cross-key delete=%d", w.Code)
	}
	if w := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses/resp_cross/cancel", otherSecret, ""); w.Code != http.StatusNotFound {
		t.Fatalf("cross-key cancel=%d", w.Code)
	}
	if w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/resp_cross", fullSecret, ""); w.Code != http.StatusNotFound {
		t.Fatalf("full access bypass=%d %s", w.Code, w.Body.String())
	}
}

func TestResponseOwnerInstanceAllowlistStillEnforced(t *testing.T) {
	f := newGatewayFixture(t, false)
	ctx := context.Background()
	existing, err := f.gateway.lifecycle.Instances().Get(ctx, f.instanceID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.gateway.lifecycle.Instances().Create(ctx, instances.CreateInput{
		ModelID: existing.ModelID, Name: "Other instance", Slug: "other-instance", RequestLogMode: "full",
	})
	if err != nil {
		t.Fatal(err)
	}
	scopedKey, scopedSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "scoped-owner", KeyType: auth.APIKeyTypeInference, OwnerUserID: &f.ownerID, InstanceIDs: []string{f.instanceID},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	requestBody := `{"model":"gateway-model","input":"scoped"}`
	responseBody := `{"id":"resp_scoped","object":"response","status":"completed"}`
	reqBody := requestBody
	respBody := responseBody
	if err := f.observability.RecordCorrelatedRequest(ctx, "lr_scoped", nil, observability.RequestRecord{
		StartedAt: now, FinishedAt: now, InstanceID: f.instanceID, Endpoint: "/v1/responses",
		StatusCode: 200, Result: "success", OwnerKind: observability.OwnerKindAPIKey, OwnerID: scopedKey.ID,
		APIKey: &observability.APIKeyRef{ID: scopedKey.ID, Name: scopedKey.Name}, RequestBody: &reqBody, ResponseBody: &respBody,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.SetOpenAIResponseID(ctx, "lr_scoped", "resp_scoped"); err != nil {
		t.Fatal(err)
	}
	otherOnly := []string{other.ID}
	if err := f.gateway.auth.UpdateAPIKey(ctx, scopedKey.ID, auth.UpdateAPIKeyInput{InstanceIDs: &otherOnly}); err != nil {
		t.Fatal(err)
	}

	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/resp_scoped", scopedSecret, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "instance_not_allowed") {
		t.Fatalf("scoped owner wrong instance=%d %s", w.Code, w.Body.String())
	}
}

func TestResponseOwnerLegacyEmptyOwnerFailsClosed(t *testing.T) {
	f := newGatewayFixture(t, false)
	ctx := context.Background()
	body := `{"id":"resp_legacy","object":"response","status":"completed"}`
	if err := f.observability.BeginCorrelatedRequest(ctx, "lr_legacy", observability.RequestRecord{
		StartedAt: 1, InstanceID: f.instanceID, Endpoint: "/v1/responses", CallType: "response",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.FinalizeCorrelatedRequest(ctx, "lr_legacy", nil, observability.RequestRecord{
		StartedAt: 1, FinishedAt: 2, InstanceID: f.instanceID, Endpoint: "/v1/responses",
		StatusCode: 200, Result: "success", ResponseBody: &body,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.SetOpenAIResponseID(ctx, "lr_legacy", "resp_legacy"); err != nil {
		t.Fatal(err)
	}
	if w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/resp_legacy", f.secret, ""); w.Code != http.StatusNotFound {
		t.Fatalf("legacy owner=%d %s", w.Code, w.Body.String())
	}
}

func TestResponseOwnerInFlightAccess(t *testing.T) {
	f := newGatewayFixture(t, true)
	server := httptest.NewServer(f.gateway)
	defer server.Close()

	ctx := context.Background()
	_, otherSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "inflight-other", KeyType: auth.APIKeyTypeInference, OwnerUserID: &f.ownerID, InstanceIDs: []string{f.instanceID},
	})
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/responses", strings.NewReader(`{"model":"gateway-model","stream":true,"slow":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.secret)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	id := parseResponseIDFromSSE(buf[:n])
	if id == "" {
		t.Fatalf("missing in-flight id: %s", buf[:n])
	}

	ownerGet := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/"+id, f.secret, "")
	if ownerGet.Code != http.StatusOK || !strings.Contains(ownerGet.Body.String(), `"in_progress"`) {
		t.Fatalf("owner in-flight get=%d %s", ownerGet.Code, ownerGet.Body.String())
	}
	otherGet := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/"+id, otherSecret, "")
	if otherGet.Code != http.StatusNotFound {
		t.Fatalf("cross-key in-flight get=%d %s", otherGet.Code, otherGet.Body.String())
	}
	otherCancel := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses/"+id+"/cancel", otherSecret, "")
	if otherCancel.Code != http.StatusNotFound {
		t.Fatalf("cross-key in-flight cancel=%d %s", otherCancel.Code, otherCancel.Body.String())
	}
	ownerCancel := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses/"+id+"/cancel", f.secret, "")
	if ownerCancel.Code != http.StatusOK {
		t.Fatalf("owner in-flight cancel=%d %s", ownerCancel.Code, ownerCancel.Body.String())
	}
}

func TestPlaygroundResponseNotReadableByInferenceKey(t *testing.T) {
	f := newGatewayFixture(t, true)
	ctx := context.Background()
	requestBody := `{"model":"gateway-model","input":"playground"}`
	responseBody := `{"id":"resp_playground","object":"response","status":"completed"}`
	now := time.Now().UnixMilli()
	reqBody := requestBody
	respBody := responseBody
	if err := f.observability.RecordCorrelatedRequest(ctx, "lr_playground", nil, observability.RequestRecord{
		StartedAt: now, FinishedAt: now, InstanceID: f.instanceID, Endpoint: "/v1/responses",
		StatusCode: 200, Result: "success", OwnerKind: observability.OwnerKindManagementUser,
		OwnerID: fmt.Sprintf("%d", f.ownerID), RequestBody: &reqBody, ResponseBody: &respBody,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.SetOpenAIResponseID(ctx, "lr_playground", "resp_playground"); err != nil {
		t.Fatal(err)
	}
	if w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/resp_playground", f.secret, ""); w.Code != http.StatusNotFound {
		t.Fatalf("inference key read playground response=%d %s", w.Code, w.Body.String())
	}
}

func TestManagementPlaygroundTrustedPrincipalStamped(t *testing.T) {
	f := newGatewayFixture(t, true)
	handler := NewManagementPlaygroundProxy(f.gateway)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playground/chat/completions", strings.NewReader(`{"model":"gateway-model","messages":[{"role":"user","content":"hello"}]}`))
	req = req.WithContext(auth.WithTrustedInferenceContext(req.Context(), auth.TrustedInferencePrincipal{
		Kind: observability.OwnerKindManagementUser,
		ID:   fmt.Sprintf("%d", f.ownerID),
	}))
	req.Header.Set("Authorization", "Bearer management-token-must-not-reach-gateway")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("playground bridge=%d %s", w.Code, w.Body.String())
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
	if err != nil {
		t.Fatal(err)
	}
	if record.Endpoint != "/v1/chat/completions" || record.InstanceID != f.instanceID || record.Result != "success" {
		t.Fatalf("playground bridge observability=%+v", record)
	}
	var ownerKind, ownerID string
	if err := f.db.QueryRowContext(context.Background(), `SELECT owner_kind,owner_id FROM inference_requests r
		JOIN inference_request_correlations c ON c.inference_request_id=r.id WHERE c.request_id=?`, w.Header().Get(headerRequestID)).Scan(&ownerKind, &ownerID); err != nil {
		t.Fatal(err)
	}
	if ownerKind != observability.OwnerKindManagementUser || ownerID != fmt.Sprintf("%d", f.ownerID) {
		t.Fatalf("playground owner=%q/%q", ownerKind, ownerID)
	}
}
