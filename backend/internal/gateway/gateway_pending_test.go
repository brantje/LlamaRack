package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/observability"
)

func TestQueueLimitRejectionIsOverloadedAndDoesNotStartWorker(t *testing.T) {
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

	w := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{"model":"gateway-model"}`)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"code":"overloaded"`) || strings.Contains(w.Body.String(), "model_unavailable") {
		t.Fatalf("overload=%d %s", w.Code, w.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	errObj, _ := payload["error"].(map[string]any)
	if errObj["type"] != "server_error" || errObj["code"] != "overloaded" {
		t.Fatalf("error envelope=%v", payload)
	}

	records, err := f.observability.ListRequests(context.Background(), observability.RequestFilters{InstanceID: instance.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if record.StatusCode == http.StatusServiceUnavailable && strings.Contains(strings.ToLower(record.Error), "pending") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing overload request log: %+v", records)
	}

	close(hold)
	if code := <-firstDone; code != http.StatusOK {
		t.Fatalf("admitted request=%d", code)
	}
}
