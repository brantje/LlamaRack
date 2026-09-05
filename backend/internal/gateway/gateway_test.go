package gateway

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/observability"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

var helperSeq atomic.Uint64

func gatewayFakeBinary(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMARACK_GATEWAY_TEST_BINARY", exe)
	t.Setenv("GO_WANT_GATEWAY_HELPER", "1")
	path := filepath.Join(t.TempDir(), "fake-llama")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec \"$LLAMARACK_GATEWAY_TEST_BINARY\" -test.run=TestGatewayHelperProcess -- \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGatewayHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_GATEWAY_HELPER") != "1" {
		return
	}
	args := os.Args
	start := 0
	for i, arg := range args {
		if arg == "--" {
			start = i + 1
			break
		}
	}
	args = args[start:]
	port := 0
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--port" {
			port, _ = strconv.Atoi(args[i+1])
		}
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "authorization leaked", 500)
			return
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"text": "transcribed", "path": r.URL.Path, "proxied": true})
			return
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(bodyBytes, &body)
		if force, _ := body["force_404"].(bool); force {
			http.NotFound(w, r)
			return
		}
		seq := helperSeq.Add(1)
		usage := map[string]any{"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}
		timings := map[string]any{"prompt_n": 2, "prompt_ms": 4, "prompt_per_second": 500, "predicted_n": 3, "predicted_ms": 6, "predicted_per_second": 500}
		switch r.URL.Path {
		case "/slots":
			if r.URL.Query().Get("force_404") == "1" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"slots": []any{}, "path": r.URL.Path, "query": r.URL.RawQuery, "proxied": true,
			})
			return
		default:
			if strings.HasPrefix(r.URL.Path, "/slots/") {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok": true, "path": r.URL.Path, "query": r.URL.RawQuery, "proxied": true,
				})
				return
			}
		}
		switch r.URL.Path {
		case "/v1/responses/input_tokens", "/v1/chat/completions/input_tokens":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list", "input_tokens": 7, "path": r.URL.Path, "proxied": true,
				"usage": map[string]any{"input_tokens": 7, "total_tokens": 7},
			})
			return
		case "/v1/rerank", "/v1/reranking":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"proxied": true, "path": r.URL.Path, "model": body["model"], "results": []any{}})
			return
		case "/v1/chat/completions/control":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": r.URL.Path, "id": body["id"], "proxied": true})
			return
		case "/v1/responses":
			id := fmt.Sprintf("resp_%d", seq)
			if streaming, _ := body["stream"].(bool); streaming {
				w.Header().Set("Content-Type", "text/event-stream")
				flusher, _ := w.(http.Flusher)
				_, _ = fmt.Fprintf(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":%q,\"object\":\"response\",\"status\":\"in_progress\"}}\n\n", id)
				if flusher != nil {
					flusher.Flush()
				}
				if slow, _ := body["slow"].(bool); slow {
					time.Sleep(1500 * time.Millisecond)
				} else {
					time.Sleep(200 * time.Millisecond)
				}
				completed := map[string]any{
					"type": "response.completed",
					"response": map[string]any{
						"id": id, "object": "response", "status": "completed",
						"output": []any{}, "usage": usage, "timings": timings,
					},
				}
				payload, _ := json.Marshal(completed)
				_, _ = fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", payload)
				if flusher != nil {
					flusher.Flush()
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": id, "object": "response", "status": "completed", "proxied": true,
				"path": r.URL.Path, "model": body["model"], "usage": usage, "timings": timings,
			})
			return
		}
		if streaming, _ := body["stream"].(bool); streaming {
			id := fmt.Sprintf("chatcmpl-%d", seq)
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprintf(w, "data: {\"id\":%q,\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n", id)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(time.Second)
			payload, _ := json.Marshal(map[string]any{"id": id, "usage": usage, "timings": timings})
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": fmt.Sprintf("chatcmpl-%d", seq), "proxied": true, "path": r.URL.Path,
			"model": body["model"], "usage": usage, "timings": timings,
		})
	})
	_ = (&http.Server{Handler: mux}).Serve(ln)
	os.Exit(0)
}

type gatewayFixture struct {
	gateway       *Gateway
	lifecycle     *lifecycle.Service
	secret        string
	ownerID       int64
	db            *sql.DB
	sup           *supervisor.Supervisor
	observability *observability.Service
}

func newGatewayFixture(t *testing.T, autoload bool) *gatewayFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	modelsDir := filepath.Join(root, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := auth.New(db, time.Hour)
	user, err := a.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := a.CreateAPIKeyForUser(ctx, "gateway", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := models.New(db, modelsDir)
	path := filepath.Join(modelsDir, "gateway-Q4_K_M.gguf")
	if err := os.WriteFile(path, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, models.CreateModelInput{PublicID: "gateway-model", Name: "Gateway model", GGUFPath: path, Autoload: &autoload}); err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New(gatewayFakeBinary(t), "127.0.0.1", 33000, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sup.Shutdown(ctx)
	})
	l := lifecycle.New(m, sup)
	obs := observability.New(db)
	return &gatewayFixture{gateway: New(a, m, l, obs), lifecycle: l, secret: secret, ownerID: user.ID, db: db, sup: sup, observability: obs}
}

func gatewayRequest(t *testing.T, g http.Handler, method, path, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func TestAuthenticationSupportedAndErrorResponses(t *testing.T) {
	f := newGatewayFixture(t, false)
	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", "", "")
	if w.Code != 401 || !strings.Contains(w.Body.String(), "invalid_api_key") {
		t.Fatalf("missing auth=%d %s", w.Code, w.Body.String())
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", "wrong", "")
	if w.Code != 401 {
		t.Fatalf("bad auth=%d", w.Code)
	}
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/unknown", f.secret, "")
	if w.Code != 404 || !strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("unknown=%d %s", w.Code, w.Body.String())
	}
	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "model_required") {
		t.Fatalf("invalid json=%d %s", w.Code, w.Body.String())
	}
	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{}`)
	if w.Code != 400 {
		t.Fatalf("missing model=%d %s", w.Code, w.Body.String())
	}
	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{"model":"missing"}`)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "model_not_found") || w.Header().Get(headerRequestID) == "" {
		t.Fatalf("missing model=%d %s headers=%v", w.Code, w.Body.String(), w.Header())
	}
	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, "{\"model\":\"gateway-model\"}")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "autoload disabled") {
		t.Fatalf("autoload disabled=%d %s", w.Code, w.Body.String())
	}
	if w.Header().Get(headerRequestID) == "" || w.Header().Get(headerInstance) != "gateway-model" || w.Header().Get(headerQueueMS) == "" {
		t.Fatalf("missing failure correlation headers: %v", w.Header())
	}

	records, err := f.observability.ListRequests(context.Background(), observability.RequestFilters{InstanceID: "gateway-model"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].StatusCode != http.StatusServiceUnavailable || records[0].Result != "error" || records[0].Error == "" {
		t.Fatalf("unavailable observability=%+v", records)
	}
	correlated, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
	if err != nil || correlated.ID != records[0].ID {
		t.Fatalf("failure correlation=%+v err=%v", correlated, err)
	}
	if records[0].RequestBody != nil || records[0].ResponseBody != nil {
		t.Fatalf("metadata mode persisted content: %+v", records[0])
	}
	active, queued := f.observability.Activity()
	if len(active) != 0 || len(queued) != 0 {
		t.Fatalf("activity leaked active=%v queued=%v", active, queued)
	}

	for _, path := range []string{"/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/embeddings"} {
		if !supported(path) {
			t.Fatalf("not supported: %s", path)
		}
	}
	if supported("/v1/nope") {
		t.Fatal("unexpected supported path")
	}
}

func TestListModelsAndSuccessfulProxy(t *testing.T) {
	f := newGatewayFixture(t, true)
	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", f.secret, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "gateway-model") || !strings.Contains(w.Body.String(), `"owned_by":"llamarack"`) {
		t.Fatalf("models=%d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/embeddings"} {
		w = gatewayRequest(t, f.gateway, http.MethodPost, path, f.secret, "{\"model\":\"gateway-model\",\"input\":\"hello\"}")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"proxied":true`) || !strings.Contains(w.Body.String(), path) {
			t.Fatalf("proxy %s=%d %s", path, w.Code, w.Body.String())
		}
		for _, header := range []string{headerRequestID, headerInstance, headerAutoloaded, headerQueueMS, headerTTFTMS, headerPromptTPS, headerPromptTokens, headerTotalTokens} {
			if w.Header().Get(header) == "" {
				t.Fatalf("%s missing %s: %v", path, header, w.Header())
			}
			if w.Header().Get("X-LlamaCPP-Manager-Request-ID") != "" || w.Header().Get("X-LlamaCPP-Manager-Instance") != "" {
				t.Fatalf("%s previous product headers present: %v", path, w.Header())
			}
		}
		assertNoUpstreamPortHeader(t, w.Header())
		if requestID := w.Header().Get(headerRequestID); !strings.HasPrefix(requestID, "lr_") {
			t.Fatalf("%s request id %q", path, requestID)
		}
		if path == "/v1/embeddings" {
			if w.Header().Get(headerGenerationTPS) != "" || w.Header().Get(headerGeneratedTokens) != "" {
				t.Fatalf("embeddings emitted generation metrics: %v", w.Header())
			}
		} else if w.Header().Get(headerGenerationTPS) == "" || w.Header().Get(headerGeneratedTokens) != "3" {
			t.Fatalf("generation metrics missing: %v", w.Header())
		}
		correlated, err := f.observability.GetRequestByRequestID(context.Background(), w.Header().Get(headerRequestID))
		if err != nil {
			t.Fatal(err)
		}
		if correlated.InstanceID != "gateway-model" || correlated.PromptTokensPerSecond == nil || correlated.GenerationTokensPerSecond == nil {
			t.Fatalf("correlated metrics=%+v", correlated)
		}
	}
	records, err := f.observability.ListRequests(context.Background(), observability.RequestFilters{InstanceID: "gateway-model", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("request history=%+v", records)
	}
	for _, record := range records {
		if record.Result != "success" || record.StatusCode != http.StatusOK || record.PromptTokens != 2 || record.GeneratedTokens != 3 || record.TotalTokens != 5 {
			t.Fatalf("request observability=%+v", record)
		}
		if record.APIKey == nil || record.APIKey.Name != "gateway" || record.APIKey.Prefix == "" {
			t.Fatalf("safe API key identity missing: %+v", record)
		}
		if record.RequestBody != nil || record.ResponseBody != nil {
			t.Fatalf("metadata mode persisted content: %+v", record)
		}
	}
	if !records[3].Autoloaded || records[3].LoadDurationMS <= 0 {
		t.Fatalf("first request should record autoload: %+v", records[3])
	}
	active, queued := f.observability.Activity()
	if len(active) != 0 || len(queued) != 0 {
		t.Fatalf("activity leaked active=%v queued=%v", active, queued)
	}
}

func TestStreamingHeadersDoNotBufferOrFabricateFinalMetrics(t *testing.T) {
	f := newGatewayFixture(t, true)
	prewarm := gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{"model":"gateway-model"}`)
	if prewarm.Code != http.StatusOK {
		t.Fatalf("prewarm=%d %s", prewarm.Code, prewarm.Body.String())
	}
	server := httptest.NewServer(f.gateway)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"gateway-model","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.secret)
	started := time.Now()
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get(headerRequestID) == "" || resp.Header.Get(headerInstance) != "gateway-model" || resp.Header.Get(headerQueueMS) == "" {
		t.Fatalf("streaming pre-response headers missing: %v", resp.Header)
	}
	for _, header := range []string{headerTTFTMS, headerPromptTPS, headerGenerationTPS, headerPromptTokens, headerGeneratedTokens, headerTotalTokens} {
		if resp.Header.Get(header) != "" {
			t.Fatalf("streaming fabricated final header %s=%q", header, resp.Header.Get(header))
		}
	}
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "hello") || time.Since(started) >= 750*time.Millisecond {
		t.Fatalf("streaming first chunk was buffered: elapsed=%v line=%q", time.Since(started), line)
	}
	if activity := f.lifecycle.Activity("gateway-model"); activity.ActiveRequests != 1 || activity.PendingRequests != 0 {
		t.Fatalf("streaming should remain active until proxy completion: %+v", activity)
	}
	_, _ = reader.ReadString(0)
	correlated, err := f.observability.GetRequestByRequestID(context.Background(), resp.Header.Get(headerRequestID))
	if err != nil {
		t.Fatal(err)
	}
	if !correlated.Streaming || correlated.TTFTMS == nil || correlated.GeneratedTokens != 3 {
		t.Fatalf("streaming final metrics not persisted: %+v", correlated)
	}
}

func TestListModelsDatabaseError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	authDB, err := database.Open(ctx, filepath.Join(root, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer authDB.Close()
	a := auth.New(authDB, time.Hour)
	user, err := a.Bootstrap(ctx, "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := a.CreateAPIKeyForUser(ctx, "gateway", user.ID)
	if err != nil {
		t.Fatal(err)
	}

	modelDB, err := database.Open(ctx, filepath.Join(root, "models.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := models.New(modelDB, filepath.Join(root, "models"))
	if err := modelDB.Close(); err != nil {
		t.Fatal(err)
	}

	sup := supervisor.New("unused", "127.0.0.1", 39000, time.Second)
	l := lifecycle.New(m, sup)
	g := New(a, m, l)
	w := gatewayRequest(t, g, http.MethodGet, "/v1/models", secret, "")
	if w.Code != 500 || !strings.Contains(w.Body.String(), "database_error") {
		t.Fatalf("models database failure=%d %s", w.Code, w.Body.String())
	}
	w = gatewayRequest(t, g, http.MethodGet, "/v1/models/gateway-model", secret, "")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "model_unavailable") {
		t.Fatalf("model lookup database failure=%d %s", w.Code, w.Body.String())
	}
}

func TestAuthenticateAndJSONHelpers(t *testing.T) {
	f := newGatewayFixture(t, false)
	if err := f.gateway.authenticate(context.Background(), "Basic abc"); err == nil {
		t.Fatal("expected bearer validation error")
	}
	if err := f.gateway.authenticate(context.Background(), "Bearer "+f.secret); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	writeError(w, 422, "invalid_request_error", "test", "message")
	if w.Code != 422 || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Body.String(), "message") {
		t.Fatalf("writeError=%d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	writeJSON(w, 201, map[string]bool{"ok": true})
	if w.Code != 201 || !strings.Contains(w.Body.String(), "true") {
		t.Fatalf("writeJSON=%d %s", w.Code, w.Body.String())
	}
}
