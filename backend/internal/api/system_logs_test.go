package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/systemlog"
)

func TestSystemLogFiltersCompose(t *testing.T) {
	store := systemlog.New(20)
	store.Add(systemlog.Info, "manager", "reconcile: 1 Always On Instance satisfied")
	store.Add(systemlog.Warn, "telemetry", "pmon fallback")
	store.Add(systemlog.Error, "qwen-ci", "exit status 1: invalid device CUDA1")
	store.Add(systemlog.Debug, "telemetry", "NSpid map: host 10 -> container 2 (CUDA0)")
	h := NewSystemLogHandler(store)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/logs?scope=system&level=WARN&q=device&limit=20", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"level":"ERROR"`) || !strings.Contains(w.Body.String(), `"source":"qwen-ci"`) || strings.Contains(w.Body.String(), "pmon fallback") {
		t.Fatalf("filtered=%d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/logs?scope=system&source=telemetry&level=DEBUG", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "NSpid map") || strings.Contains(w.Body.String(), "reconcile") {
		t.Fatalf("source filter=%d %s", w.Code, w.Body.String())
	}
}

func TestSystemLogValidationAndPhase11Dispatch(t *testing.T) {
	for _, path := range []string{
		"/api/v1/logs?scope=system&limit=0",
		"/api/v1/logs?scope=system&limit=4001",
		"/api/v1/logs?scope=system&limit=nope",
		"/api/v1/logs?scope=system&level=TRACE",
	} {
		w := httptest.NewRecorder()
		NewSystemLogHandler(systemlog.New(2)).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s => %d %s", path, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	NewSystemLogHandler(systemlog.New(2)).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/logs?scope=system", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", w.Code)
	}

	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	systemlog.Log(systemlog.Info, "manager", "aggregate route works")
	w = httptest.NewRecorder()
	NewPhase11LogHandler(&fakePhase11Logs{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/logs?scope=system", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "aggregate route works") {
		t.Fatalf("dispatch=%d %s", w.Code, w.Body.String())
	}
}

func TestSystemLogStream(t *testing.T) {
	store := systemlog.New(10)
	store.Add(systemlog.Info, "manager", "old")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream?scope=system&source=manager&limit=10", nil).WithContext(ctx)
	NewSystemLogHandler(store).ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(w.Body.String(), "event: log") || !strings.Contains(w.Body.String(), `"message":"old"`) || !strings.Contains(w.Body.String(), ": connected") {
		t.Fatalf("stream=%d %s", w.Code, w.Body.String())
	}
}
