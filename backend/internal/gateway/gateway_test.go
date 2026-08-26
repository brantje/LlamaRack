package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/database"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func gatewayFakeBinary(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil { t.Fatal(err) }
	t.Setenv("LCM_GATEWAY_TEST_BINARY", exe)
	t.Setenv("GO_WANT_GATEWAY_HELPER", "1")
	path := filepath.Join(t.TempDir(), "fake-llama")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec \"$LCM_GATEWAY_TEST_BINARY\" -test.run=TestGatewayHelperProcess -- \"$@\"\n"), 0o755); err != nil { t.Fatal(err) }
	return path
}

func TestGatewayHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_GATEWAY_HELPER") != "1" { return }
	args := os.Args
	start := 0
	for i, arg := range args { if arg == "--" { start = i + 1; break } }
	args = args[start:]
	port := 0
	for i := 0; i+1 < len(args); i++ { if args[i] == "--port" { port, _ = strconv.Atoi(args[i+1]) } }
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil { os.Exit(2) }
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" { http.Error(w, "authorization leaked", 500); return }
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"proxied": true, "path": r.URL.Path, "model": body["model"]})
	})
	_ = (&http.Server{Handler: mux}).Serve(ln)
	os.Exit(0)
}

type gatewayFixture struct {
	gateway *Gateway
	secret string
	sup *supervisor.Supervisor
}

func newGatewayFixture(t *testing.T, autoload bool) *gatewayFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	modelsDir := filepath.Join(root, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil { t.Fatal(err) }
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func(){ _ = db.Close() })
	a := auth.New(db, time.Hour)
	_, secret, err := a.CreateAPIKey(ctx, "gateway")
	if err != nil { t.Fatal(err) }
	m := models.New(db, modelsDir)
	path := filepath.Join(modelsDir, "gateway-Q4_K_M.gguf")
	if err := os.WriteFile(path, []byte("gguf"), 0o644); err != nil { t.Fatal(err) }
	if _, err := m.Create(ctx, models.CreateModelInput{PublicID:"gateway-model", Name:"Gateway model", GGUFPath:path, Autoload:&autoload}); err != nil { t.Fatal(err) }
	sup := supervisor.New(gatewayFakeBinary(t), "127.0.0.1", 33000, 5*time.Second)
	t.Cleanup(func(){ ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second); defer cancel(); sup.Shutdown(ctx) })
	l := lifecycle.New(m, sup)
	return &gatewayFixture{gateway: New(a, m, l), secret: secret, sup: sup}
}

func gatewayRequest(t *testing.T, g http.Handler, method, path, secret, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if secret != "" { r.Header.Set("Authorization", "Bearer "+secret) }
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func TestAuthenticationSupportedAndErrorResponses(t *testing.T) {
	f := newGatewayFixture(t, false)
	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", "", "")
	if w.Code != 401 || !strings.Contains(w.Body.String(), "invalid_api_key") { t.Fatalf("missing auth=%d %s", w.Code, w.Body.String()) }
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", "wrong", "")
	if w.Code != 401 { t.Fatalf("bad auth=%d", w.Code) }
	w = gatewayRequest(t, f.gateway, http.MethodGet, "/v1/unknown", f.secret, "")
	if w.Code != 404 || !strings.Contains(w.Body.String(), "not_found") { t.Fatalf("unknown=%d %s", w.Code, w.Body.String()) }
	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "model_required") { t.Fatalf("invalid json=%d %s", w.Code, w.Body.String()) }
	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{}`)
	if w.Code != 400 { t.Fatalf("missing model=%d %s", w.Code, w.Body.String()) }
	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, `{"model":"missing"}`)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "model_unavailable") { t.Fatalf("missing model=%d %s", w.Code, w.Body.String()) }
	w = gatewayRequest(t, f.gateway, http.MethodPost, "/v1/chat/completions", f.secret, "{\"model\":\"gateway-model\"}")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "autoload disabled") { t.Fatalf("autoload disabled=%d %s", w.Code, w.Body.String()) }
	for _, path := range []string{"/v1/chat/completions","/v1/completions","/v1/responses","/v1/embeddings"} { if !supported(path) { t.Fatalf("not supported: %s", path) } }
	if supported("/v1/nope") { t.Fatal("unexpected supported path") }
}

func TestListModelsAndSuccessfulProxy(t *testing.T) {
	f := newGatewayFixture(t, true)
	w := gatewayRequest(t, f.gateway, http.MethodGet, "/v1/models", f.secret, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "gateway-model") || !strings.Contains(w.Body.String(), "llamacpp-manager") { t.Fatalf("models=%d %s", w.Code, w.Body.String()) }
	for _, path := range []string{"/v1/chat/completions","/v1/completions","/v1/responses","/v1/embeddings"} {
		w = gatewayRequest(t, f.gateway, http.MethodPost, path, f.secret, "{\"model\":\"gateway-model\",\"input\":\"hello\"}")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"proxied":true`) || !strings.Contains(w.Body.String(), path) { t.Fatalf("proxy %s=%d %s", path, w.Code, w.Body.String()) }
	}
}

func TestListModelsDatabaseError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	authDB, err := database.Open(ctx, filepath.Join(root, "auth.db"))
	if err != nil { t.Fatal(err) }
	defer authDB.Close()
	a := auth.New(authDB, time.Hour)
	_, secret, err := a.CreateAPIKey(ctx, "gateway")
	if err != nil { t.Fatal(err) }

	modelDB, err := database.Open(ctx, filepath.Join(root, "models.db"))
	if err != nil { t.Fatal(err) }
	m := models.New(modelDB, filepath.Join(root, "models"))
	if err := modelDB.Close(); err != nil { t.Fatal(err) }

	sup := supervisor.New("unused", "127.0.0.1", 39000, time.Second)
	l := lifecycle.New(m, sup)
	g := New(a, m, l)
	w := gatewayRequest(t, g, http.MethodGet, "/v1/models", secret, "")
	if w.Code != 500 || !strings.Contains(w.Body.String(), "database_error") {
		t.Fatalf("models database failure=%d %s", w.Code, w.Body.String())
	}
}

func TestAuthenticateAndJSONHelpers(t *testing.T) {
	f := newGatewayFixture(t, false)
	if err := f.gateway.authenticate(context.Background(), "Basic abc"); err == nil { t.Fatal("expected bearer validation error") }
	if err := f.gateway.authenticate(context.Background(), "Bearer "+f.secret); err != nil { t.Fatal(err) }
	w := httptest.NewRecorder()
	writeError(w, 422, "invalid_request_error", "test", "message")
	if w.Code != 422 || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Body.String(), "message") { t.Fatalf("writeError=%d %s", w.Code, w.Body.String()) }
	w = httptest.NewRecorder()
	writeJSON(w, 201, map[string]bool{"ok": true})
	if w.Code != 201 || !strings.Contains(w.Body.String(), "true") { t.Fatalf("writeJSON=%d %s", w.Code, w.Body.String()) }
}
