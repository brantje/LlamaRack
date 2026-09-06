package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
)

func TestClassifySlotsRoutes(t *testing.T) {
	spec, _, ok := classify(http.MethodGet, "/v1/slots")
	if !ok || spec.Kind != routeSlotsProxy || spec.CallType != "slots_list" || !spec.MapNotImplemented || spec.NeedsAcquire {
		t.Fatalf("GET slots=%+v ok=%v", spec, ok)
	}
	var params map[string]string
	spec, params, ok = classify(http.MethodPost, "/v1/slots/2")
	if !ok || spec.Kind != routeSlotsProxy || spec.CallType != "slots_action" || params["slot_id"] != "2" {
		t.Fatalf("POST slots=%+v params=%v ok=%v", spec, params, ok)
	}
}

func TestSlotsGatewayValidationAndAuth(t *testing.T) {
	f := newGatewayFixture(t, true)
	ctx := t.Context()
	instance, err := f.lifecycle.Instances().GetBySlug(ctx, "gateway-model")
	if err != nil {
		t.Fatal(err)
	}

	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/slots", f.secret, "")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "model_required") {
		t.Fatalf("missing model=%d %s", w.Code, w.Body.String())
	}

	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/slots?model=gateway-model", f.secret, "")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "model_unavailable") {
		t.Fatalf("not ready=%d %s", w.Code, w.Body.String())
	}
	if _, ok := f.sup.Endpoint(instance.ID); ok {
		t.Fatal("slots request started worker")
	}

	_, managementSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "mgmt-slots", KeyType: auth.APIKeyTypeManagement, OwnerUserID: &f.ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/slots?model=gateway-model", managementSecret, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "management_key_not_allowed") {
		t.Fatalf("management key=%d %s", w.Code, w.Body.String())
	}

	_, allowedSecret, err := f.gateway.auth.CreateAPIKey(ctx, auth.CreateAPIKeyInput{
		Name: "slots-allow", KeyType: auth.APIKeyTypeInference, OwnerUserID: &f.ownerID, InstanceIDs: []string{instance.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	const otherID = "00000000-0000-4000-8000-000000000098"
	if _, err := f.db.ExecContext(ctx, `INSERT INTO instances(id,slug,model_id,name) VALUES(?,?,?,'Other')`, otherID, "other-instance", instance.ModelID); err != nil {
		t.Fatal(err)
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/slots?model=other-instance", allowedSecret, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "instance_not_allowed") {
		t.Fatalf("allowlist miss=%d %s", w.Code, w.Body.String())
	}

	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/slots/1?model=gateway-model&action=drop", f.secret, `{}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unsupported slots action") {
		t.Fatalf("invalid action=%d %s", w.Code, w.Body.String())
	}

	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/slots/1?model=gateway-model&action=save", f.secret, `{"filename":"../escape.json"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "path separators") {
		t.Fatalf("bad filename=%d %s", w.Code, w.Body.String())
	}
}

func TestSlotsGatewayProxyReadyInstance(t *testing.T) {
	f := newGatewayFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	instance, err := f.lifecycle.Instances().GetBySlug(ctx, "gateway-model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.lifecycle.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}

	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/slots?model=gateway-model&action=save", f.secret, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"path":"/slots"`) || strings.Contains(w.Body.String(), "model=") {
		t.Fatalf("GET slots=%d %s", w.Code, w.Body.String())
	}

	for _, action := range []string{"save", "restore", "erase"} {
		body := `{}`
		if action == "save" || action == "restore" {
			body = `{"filename":"slot.json"}`
		}
		path := "/v1/slots/3?model=gateway-model&action=" + action
		w = gatewayRequest(t, f.gateway, http.MethodPost, path, f.secret, body)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"path":"/slots/3"`) {
			t.Fatalf("%s=%d %s", action, w.Code, w.Body.String())
		}
	}
}

func TestSlotsGatewayMapsWorkerNotImplemented(t *testing.T) {
	f := newGatewayFixture(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	instance, err := f.lifecycle.Instances().GetBySlug(ctx, "gateway-model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.lifecycle.StartInstance(ctx, instance.ID); err != nil {
		t.Fatal(err)
	}

	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/slots?model=gateway-model&force_404=1", f.secret, "")
	if w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), "not_implemented") {
		t.Fatalf("slots 501 mapping=%d %s", w.Code, w.Body.String())
	}
}

func TestSlotsDoesNotConsumePendingAdmission(t *testing.T) {
	f := newGatewayFixture(t, true)
	instance, err := f.lifecycle.Instances().GetBySlug(context.Background(), "gateway-model")
	if err != nil {
		t.Fatal(err)
	}
	f.lifecycle.SetPendingLimits(func(context.Context) (int, int) { return 1, 10 })
	hold := make(chan struct{})
	f.lifecycle.SetLoadHold(func(string) { <-hold })

	firstDone := make(chan int, 1)
	go func() {
		w := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{"model":"gateway-model"}`)
		firstDone <- w.Code
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && f.lifecycle.Activity(instance.ID).PendingRequests != 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if f.lifecycle.Activity(instance.ID).PendingRequests != 1 {
		t.Fatal("first request did not become pending")
	}

	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/slots?model=gateway-model", f.secret, "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("slots while unloaded=%d %s", w.Code, w.Body.String())
	}
	if f.lifecycle.Activity(instance.ID).PendingRequests != 1 {
		t.Fatalf("slots poll changed pending count: %+v", f.lifecycle.Activity(instance.ID))
	}

	close(hold)
	if code := <-firstDone; code != http.StatusOK {
		t.Fatalf("admitted request=%d", code)
	}
}
