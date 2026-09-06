package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/observability"
)

func TestClassifyRoutes(t *testing.T) {
	spec, params, ok := classify(http.MethodGet, "/v1/models/gateway-model")
	if !ok || spec.Kind != routeGetModel || params["model"] != "gateway-model" {
		t.Fatalf("get model=%+v params=%v ok=%v", spec, params, ok)
	}
	spec, _, ok = classify(http.MethodPost, "/v1/responses/input_tokens")
	if !ok || spec.CallType != "response_input_tokens" || !spec.MapNotImplemented {
		t.Fatalf("input tokens=%+v", spec)
	}
	spec, params, ok = classify(http.MethodGet, "/v1/responses/resp_1/input_items")
	if !ok || spec.Kind != routeGetInputItems || params["response_id"] != "resp_1" {
		t.Fatalf("input items=%+v params=%v", spec, params)
	}
	if _, _, ok := classify(http.MethodGet, "/v1/chat/completions"); ok {
		t.Fatal("GET chat should be unknown")
	}
}

func TestParseFinalResponseJSONAndSSE(t *testing.T) {
	direct, ok := parseFinalResponseJSON([]byte(`{"id":"resp_1","object":"response","status":"completed"}`))
	if !ok || !strings.Contains(string(direct), `"resp_1"`) {
		t.Fatalf("direct=%s ok=%v", direct, ok)
	}
	sse := "event: response.created\ndata: {\"response\":{\"id\":\"resp_2\",\"object\":\"response\",\"status\":\"in_progress\"}}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_2\",\"object\":\"response\",\"status\":\"completed\"}}\n\n"
	final, ok := parseFinalResponseJSON([]byte(sse))
	if !ok || !strings.Contains(string(final), `"completed"`) || parseResponseIDFromSSE([]byte(sse)) != "resp_2" {
		t.Fatalf("sse=%s id=%s", final, parseResponseIDFromSSE([]byte(sse)))
	}
	if _, ok := parseFinalResponseJSON([]byte("not-json")); ok {
		t.Fatal("malformed should fail")
	}
}

func TestInputItemsPagination(t *testing.T) {
	items := normalizeInputItems([]byte(`{"input":["a","b","c"]}`))
	if len(items) != 3 || items[0]["id"] != "msg_0" {
		t.Fatalf("items=%v", items)
	}
	page := inputItemsList(items, "msg_0", 1)
	data := page["data"].([]map[string]any)
	if len(data) != 1 || data[0]["id"] != "msg_1" || page["has_more"] != true {
		t.Fatalf("page=%v", page)
	}
	stringItems := normalizeInputItems([]byte(`{"input":"hello"}`))
	if len(stringItems) != 1 || stringItems[0]["role"] != "user" {
		t.Fatalf("string input=%v", stringItems)
	}
}

func TestRetrieveKnownEnabledModelWithoutAcquire(t *testing.T) {
	f := newGatewayFixture(t, false)
	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models/gateway-model", f.secret, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"gateway-model"`) || !strings.Contains(w.Body.String(), `"owned_by":"llamarack"`) {
		t.Fatalf("retrieve=%d %s", w.Code, w.Body.String())
	}
	if _, ok := f.sup.Endpoint(f.instanceID); ok {
		t.Fatal("model retrieve started a worker")
	}
	unknown := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models/missing", f.secret, "")
	if unknown.Code != 404 || !strings.Contains(unknown.Body.String(), "model_not_found") {
		t.Fatalf("unknown=%d %s", unknown.Code, unknown.Body.String())
	}
	enabled := false
	if _, err := f.gateway.lifecycle.Instances().Update(context.Background(), f.instanceID, instances.UpdateInput{
		Name: "Gateway model", Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	disabled := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models/gateway-model", f.secret, "")
	if disabled.Code != 404 {
		t.Fatalf("disabled=%d %s", disabled.Code, disabled.Body.String())
	}
}

func TestTokenCountRerankAndNotImplemented(t *testing.T) {
	f := newGatewayFixture(t, true)
	for _, path := range []string{"/v1/responses/input_tokens", "/v1/chat/completions/input_tokens"} {
		w := gatewayRequest(t, f.gateway, http.MethodPost, path, f.secret, `{"model":"gateway-model"}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"proxied":true`) {
			t.Fatalf("%s=%d %s", path, w.Code, w.Body.String())
		}
		if w.Header().Get(headerGenerationTPS) != "" || w.Header().Get(headerGeneratedTokens) != "" {
			t.Fatalf("%s generation metrics=%v", path, w.Header())
		}
		record, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
		if err != nil {
			t.Fatal(err)
		}
		if record.CallType != callType(path) || record.PromptTokens != 7 {
			t.Fatalf("%s observability=%+v", path, record)
		}
	}
	missing := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses/input_tokens", f.secret, `{"model":"gateway-model","force_404":true}`)
	if missing.Code != http.StatusNotImplemented || !strings.Contains(missing.Body.String(), "not_implemented") {
		t.Fatalf("501 mapping=%d %s", missing.Code, missing.Body.String())
	}
	for _, path := range []string{"/v1/rerank", "/v1/reranking"} {
		w := gatewayRequest(t, f.gateway, http.MethodPost, path, f.secret, `{"model":"gateway-model","query":"q","documents":["a"]}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), path) {
			t.Fatalf("%s=%d %s", path, w.Code, w.Body.String())
		}
		record, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
		if err != nil || record.CallType != "rerank" {
			t.Fatalf("%s call type=%+v err=%v", path, record, err)
		}
	}
}

func TestStoredResponsesFullAndMetadata(t *testing.T) {
	f := newGatewayFixture(t, true)
	full := "full"
	if _, err := f.gateway.lifecycle.Instances().Update(context.Background(), f.instanceID, instances.UpdateInput{
		Name: "Gateway model", RequestLogMode: full,
	}); err != nil {
		t.Fatal(err)
	}
	created := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses", f.secret, `{"model":"gateway-model","input":"hello","store":false}`)
	if created.Code != 200 {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	id, _ := payload["id"].(string)
	if id == "" {
		t.Fatalf("missing response id: %s", created.Body.String())
	}
	got := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/"+id, f.secret, "")
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"object":"response"`) || strings.Contains(got.Body.String(), "event:") {
		t.Fatalf("retrieve=%d %s", got.Code, got.Body.String())
	}
	items := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/"+id+"/input_items", f.secret, "")
	if items.Code != 200 || !strings.Contains(items.Body.String(), `"hello"`) {
		t.Fatalf("input items=%d %s", items.Code, items.Body.String())
	}
	deleted := gatewayRequest(t, f.gateway, http.MethodDelete, "/v1/responses/"+id, f.secret, "")
	if deleted.Code != 200 || !strings.Contains(deleted.Body.String(), `"deleted":true`) {
		t.Fatalf("delete=%d %s", deleted.Code, deleted.Body.String())
	}
	after := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/"+id, f.secret, "")
	if after.Code != 404 {
		t.Fatalf("get after delete=%d %s", after.Code, after.Body.String())
	}
	log, err := f.observability.GetRequestByRequestID(context.Background(), created.Header().Get(headerRequestID))
	if err != nil || log.RequestBody == nil || log.ResponseBody == nil {
		t.Fatalf("logs after openai delete=%+v err=%v", log, err)
	}

	metadata := "metadata"
	if _, err := f.gateway.lifecycle.Instances().Update(context.Background(), f.instanceID, instances.UpdateInput{
		Name: "Gateway model", RequestLogMode: metadata,
	}); err != nil {
		t.Fatal(err)
	}
	meta := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses", f.secret, `{"model":"gateway-model","input":"later","store":true}`)
	if meta.Code != 200 {
		t.Fatalf("metadata create=%d %s", meta.Code, meta.Body.String())
	}
	var metaPayload map[string]any
	_ = json.Unmarshal(meta.Body.Bytes(), &metaPayload)
	metaID, _ := metaPayload["id"].(string)
	if metaID == "" {
		t.Fatal("metadata response id missing")
	}
	if w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/"+metaID, f.secret, ""); w.Code != 404 {
		t.Fatalf("metadata retrieve=%d %s", w.Code, w.Body.String())
	}
	if w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/"+id, f.secret, ""); w.Code != 404 {
		t.Fatalf("historical retrieve after mode change=%d", w.Code)
	}
	if w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/missing", f.secret, ""); w.Code != 404 {
		t.Fatalf("unknown retrieve=%d", w.Code)
	}
}

func TestStreamingResponseRetrieveAsJSON(t *testing.T) {
	f := newGatewayFixture(t, true)
	if _, err := f.gateway.lifecycle.Instances().Update(context.Background(), f.instanceID, instances.UpdateInput{
		Name: "Gateway model", RequestLogMode: "full",
	}); err != nil {
		t.Fatal(err)
	}
	created := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses", f.secret, `{"model":"gateway-model","input":"hello","stream":true}`)
	if created.Code != 200 || !strings.Contains(created.Body.String(), "event:") {
		t.Fatalf("stream create=%d %s", created.Code, created.Body.String())
	}
	id := parseResponseIDFromSSE(created.Body.Bytes())
	if id == "" {
		t.Fatalf("missing stream id: %s", created.Body.String())
	}
	got := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/"+id, f.secret, "")
	if got.Code != 200 || strings.Contains(got.Body.String(), "event:") || !strings.Contains(got.Body.String(), `"status":"completed"`) {
		t.Fatalf("stream retrieve=%d %s", got.Code, got.Body.String())
	}
}

func TestDuplicateOpenAIResponseIDRejectedByIndex(t *testing.T) {
	f := newGatewayFixture(t, false)
	ctx := context.Background()
	now := time.Now().UnixMilli()
	body := `{"id":"resp_dup","object":"response"}`
	if err := f.observability.RecordCorrelatedRequest(ctx, "lr_dup_1", nil, observability.RequestRecord{
		StartedAt: now, FinishedAt: now, InstanceID: f.instanceID, Endpoint: "/v1/responses",
		StatusCode: 200, Result: "success", ResponseBody: &body,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.SetOpenAIResponseID(ctx, "lr_dup_1", "resp_dup"); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.RecordCorrelatedRequest(ctx, "lr_dup_2", nil, observability.RequestRecord{
		StartedAt: now, FinishedAt: now, InstanceID: f.instanceID, Endpoint: "/v1/responses",
		StatusCode: 200, Result: "success", ResponseBody: &body,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.SetOpenAIResponseID(ctx, "lr_dup_2", "resp_dup"); err != observability.ErrDuplicateOpenAIResponseID {
		t.Fatalf("duplicate=%v", err)
	}
}

func TestRetentionRemovesRetrievableResponse(t *testing.T) {
	f := newGatewayFixture(t, false)
	ctx := context.Background()
	body := `{"id":"resp_old","object":"response","status":"completed"}`
	if err := f.observability.RecordCorrelatedRequest(ctx, "lr_old", nil, observability.RequestRecord{
		StartedAt: 1, FinishedAt: 2, InstanceID: f.instanceID, Endpoint: "/v1/responses",
		StatusCode: 200, Result: "success", ResponseBody: &body,
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.SetOpenAIResponseID(ctx, "lr_old", "resp_old"); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.Prune(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/resp_old", f.secret, ""); w.Code != 404 {
		t.Fatalf("pruned retrieve=%d %s", w.Code, w.Body.String())
	}
}

func TestMalformedStoredResponseFailsSafe(t *testing.T) {
	f := newGatewayFixture(t, false)
	ctx := context.Background()
	if err := f.observability.BeginCorrelatedRequest(ctx, "lr_malformed", observability.RequestRecord{
		StartedAt: 1, InstanceID: f.instanceID, Endpoint: "/v1/responses", CallType: "response",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.SetOpenAIResponseID(ctx, "lr_malformed", "resp_bad"); err != nil {
		t.Fatal(err)
	}
	if err := f.observability.FinalizeCorrelatedRequest(ctx, "lr_malformed", nil, observability.RequestRecord{
		StartedAt: 1, FinishedAt: 2, InstanceID: f.instanceID, Endpoint: "/v1/responses",
		StatusCode: 200, Result: "success", ResponseBody: strPtr("not-json"),
	}); err != nil {
		t.Fatal(err)
	}
	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/resp_bad", f.secret, "")
	if w.Code != 404 {
		t.Fatalf("malformed=%d %s", w.Code, w.Body.String())
	}
}

func TestAudioTranscriptionMultipart(t *testing.T) {
	f := newGatewayFixture(t, true)
	if _, err := f.gateway.lifecycle.Instances().Update(context.Background(), f.instanceID, instances.UpdateInput{
		Name: "Gateway model", RequestLogMode: "full",
	}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("model", "gateway-model")
	file, err := writer.CreateFormFile("file", "clip.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("RIFF....WAVE")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Authorization", "Bearer "+f.secret)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	f.gateway.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"text":"transcribed"`) {
		t.Fatalf("transcription=%d %s", w.Code, w.Body.String())
	}
	record, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
	if err != nil {
		t.Fatal(err)
	}
	if record.CallType != "transcription" || record.RequestBody == nil || strings.Contains(*record.RequestBody, "RIFF") {
		t.Fatalf("logged body=%+v", record)
	}
	if !strings.Contains(*record.RequestBody, `"filename":"clip.wav"`) || !strings.Contains(*record.RequestBody, `"size":`) {
		t.Fatalf("structured log=%s", *record.RequestBody)
	}
	missing := multipartRequest(t, f, map[string]string{}, "clip.wav", []byte("audio"))
	if missing.Code != 400 || !strings.Contains(missing.Body.String(), "model_required") {
		t.Fatalf("missing model=%d %s", missing.Code, missing.Body.String())
	}
}

func TestAudioTranscriptionOversized(t *testing.T) {
	f := newGatewayFixture(t, false)
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1)))
	req.Header.Set("Authorization", "Bearer "+f.secret)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=abc")
	w := httptest.NewRecorder()
	f.gateway.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize=%d %s", w.Code, w.Body.String())
	}
}

func TestChatControlRoutesToOwningInstance(t *testing.T) {
	f := newGatewayFixture(t, true)
	server := httptest.NewServer(f.gateway)
	defer server.Close()
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gateway-model","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.secret)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var collected []byte
	buf := make([]byte, 64)
	id := ""
	for id == "" {
		n, readErr := resp.Body.Read(buf)
		collected = append(collected, buf[:n]...)
		id = parseResponseIDFromSSE(collected)
		if readErr != nil {
			break
		}
	}
	if id == "" {
		t.Fatalf("missing completion id: %s", collected)
	}
	control := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions/control", f.secret, fmt.Sprintf(`{"id":%q,"model":"missing"}`, id))
	if control.Code != 200 || !strings.Contains(control.Body.String(), `"ok":true`) {
		t.Fatalf("control=%d %s", control.Code, control.Body.String())
	}
	unknown := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions/control", f.secret, `{"id":"missing"}`)
	if unknown.Code != 404 {
		t.Fatalf("unknown control=%d %s", unknown.Code, unknown.Body.String())
	}
}

func TestInFlightResponseGetAndCancel(t *testing.T) {
	f := newGatewayFixture(t, true)
	server := httptest.NewServer(f.gateway)
	defer server.Close()
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
	inProgress := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/responses/"+id, f.secret, "")
	if inProgress.Code != 200 || !strings.Contains(inProgress.Body.String(), `"in_progress"`) {
		t.Fatalf("in-flight get=%d %s", inProgress.Code, inProgress.Body.String())
	}
	cancelled := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses/"+id+"/cancel", f.secret, "")
	if cancelled.Code != 200 || !strings.Contains(cancelled.Body.String(), `"status":"cancelled"`) && !strings.Contains(cancelled.Body.String(), `"id"`) {
		t.Fatalf("cancel=%d %s", cancelled.Code, cancelled.Body.String())
	}
	again := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses/"+id+"/cancel", f.secret, "")
	if again.Code != 400 && again.Code != 404 {
		t.Fatalf("second cancel=%d %s", again.Code, again.Body.String())
	}
	unknown := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses/missing/cancel", f.secret, "")
	if unknown.Code != 404 {
		t.Fatalf("unknown cancel=%d", unknown.Code)
	}
	completed := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses", f.secret, `{"model":"gateway-model","input":"done"}`)
	var payload map[string]any
	_ = json.Unmarshal(completed.Body.Bytes(), &payload)
	doneID, _ := payload["id"].(string)
	if w := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/responses/"+doneID+"/cancel", f.secret, ""); w.Code != 400 {
		t.Fatalf("completed cancel=%d %s", w.Code, w.Body.String())
	}
}

func TestActiveRegistryCleanup(t *testing.T) {
	reg := newActiveRegistry()
	reg.register(&activeRequest{managerRequestID: "lr_1", upstreamID: "resp_1", instanceID: "one"})
	if got := reg.getByUpstream("resp_1"); got == nil || got.instanceID != "one" {
		t.Fatalf("lookup=%+v", got)
	}
	reg.remove("lr_1")
	if got := reg.getByUpstream("resp_1"); got != nil {
		t.Fatalf("leaked=%+v", got)
	}
}

func multipartRequest(t *testing.T, f *gatewayFixture, fields map[string]string, filename string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range fields {
		_ = writer.WriteField(key, value)
	}
	if filename != "" {
		file, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write(data)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Authorization", "Bearer "+f.secret)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	f.gateway.ServeHTTP(w, req)
	return w
}

func strPtr(value string) *string { return &value }

func TestParseMultipartModel(t *testing.T) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("model", "gateway-model")
	file, _ := writer.CreateFormFile("file", "a.wav")
	_, _ = file.Write([]byte("abc"))
	_ = writer.Close()
	model, logJSON, err := parseMultipartModel(buf.Bytes(), writer.FormDataContentType())
	if err != nil || model != "gateway-model" || !strings.Contains(logJSON, `"filename":"a.wav"`) || strings.Contains(logJSON, "abc") {
		t.Fatalf("model=%q log=%s err=%v", model, logJSON, err)
	}
}
