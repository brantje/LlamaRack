package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/observability"
)

func TestCoverageHelpersAndLocalHandlers(t *testing.T) {
	if extractQuotedID([]byte(`prefix "id":"resp_quoted" suffix`)) != "resp_quoted" {
		t.Fatal("quoted id")
	}
	if extractQuotedID([]byte(`"id":"`)) != "" || extractQuotedID([]byte("nope")) != "" {
		t.Fatal("incomplete quoted id")
	}
	if parseLimitQuery("") != defaultInputItemLimit || parseLimitQuery("nope") != defaultInputItemLimit || parseLimitQuery("5") != 5 {
		t.Fatal("limit query")
	}
	items := normalizeInputItems([]byte(`{"input":[{"type":"message","content":"x"},{"id":"keep","type":"message"}]}`))
	if len(items) != 2 || items[0]["id"] != "msg_0" || items[1]["id"] != "keep" {
		t.Fatalf("object items=%v", items)
	}
	if normalizeInputItems([]byte(`{"input":1}`)) != nil || normalizeInputItems([]byte("not-json")) != nil {
		t.Fatal("invalid input")
	}
	page, more := paginateInputItems(items, "missing", 1000)
	if more || len(page) != 0 {
		t.Fatalf("missing after=%v more=%v", page, more)
	}
	if _, _, err := parseMultipartModel(nil, "application/json"); err == nil || err.Error() != "invalid multipart body" {
		t.Fatalf("invalid multipart=%v", err)
	}
	if classifyCall := callType("/v1/responses/resp_1"); classifyCall != "response_retrieve" {
		t.Fatalf("get call type=%q", classifyCall)
	}
	if !supported("/v1/audio/transcriptions") || supported("/v1/nope") {
		t.Fatal("supported")
	}
	if _, _, ok := classify(http.MethodGet, ""); ok {
		t.Fatal("empty path")
	}
	if params, ok := matchPath("/v1/models/{model}", "/v1/models/gw%2Fmodel"); !ok || params["model"] != "gw/model" {
		t.Fatalf("unescape=%v ok=%v", params, ok)
	}

	reg := newActiveRegistry()
	reg.register(nil)
	reg.setUpstreamID("", "")
	if reg.getByUpstream("") != nil {
		t.Fatal("empty lookup")
	}
	reg.register(&activeRequest{managerRequestID: "lr_1", cancel: func() {}})
	reg.setUpstreamID("lr_1", "resp_1")
	reg.setUpstreamID("lr_1", "resp_2")
	if _, ok := reg.cancelByUpstream("resp_2"); !ok {
		t.Fatal("cancel")
	}
	if _, ok := reg.cancelByUpstream("resp_2"); ok {
		t.Fatal("second cancel should be already cancelled")
	}
	reg.remove("lr_1")
	reg.remove("missing")
	if reg.waitRemoved("gone", time.Millisecond) != true {
		t.Fatal("already removed")
	}
	reg.register(&activeRequest{managerRequestID: "lr_wait"})
	if reg.waitRemoved("lr_wait", 15*time.Millisecond) {
		t.Fatal("timeout expected")
	}
	reg.remove("lr_wait")

	g := &Gateway{active: newActiveRegistry()}
	w := httptest.NewRecorder()
	g.deleteStoredResponse(w, httptest.NewRequest(http.MethodDelete, "/v1/responses/x", nil), "x")
	if w.Code != 404 {
		t.Fatalf("nil obs delete=%d", w.Code)
	}
	if _, err := g.lookupStoredResponse(context.Background(), "x"); err == nil {
		t.Fatal("nil obs lookup")
	}
	w = httptest.NewRecorder()
	g.getResponseInputItems(w, httptest.NewRequest(http.MethodGet, "/v1/responses/x/input_items", nil), "x")
	if w.Code != 404 {
		t.Fatalf("nil obs items=%d", w.Code)
	}
	g.persistOpenAIResponseID(context.Background(), "lr", "")
	g.persistOpenAIResponseID(context.Background(), "lr", "resp_1")

	target, _ := url.Parse("http://127.0.0.1:9")
	g.active.register(&activeRequest{managerRequestID: "lr_c", instanceID: "gateway-model", target: target, upstreamID: "cmpl_1", endpoint: "/v1/chat/completions"})
	w = httptest.NewRecorder()
	record := observability.RequestRecord{Endpoint: "/v1/chat/completions/control"}
	var tps *float64
	var panicVal any
	g.proxyChatControl(newResponseObserver(w, false), httptest.NewRequest(http.MethodPost, "/v1/chat/completions/control", nil), routeSpec{Kind: routeChatControl}, "lr_ctl", &record, []byte(`{"id":"missing"}`), time.Now(), &tps, &panicVal)
	if w.Code != 404 {
		t.Fatalf("control missing=%d", w.Code)
	}
}
