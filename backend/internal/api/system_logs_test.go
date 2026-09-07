package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/resourceid"
	"github.com/brantje/llamarack/backend/internal/supervisor"
	"github.com/brantje/llamarack/backend/internal/systemlog"
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

func TestSystemLogValidationAndDispatch(t *testing.T) {
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
	NewInstanceLogHandler(&fakeInstanceLogs{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/logs?scope=system", nil))
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
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(w.Body.String(), "event: snapshot") || !strings.Contains(w.Body.String(), `"message":"old"`) || !strings.Contains(w.Body.String(), ": connected") {
		t.Fatalf("stream=%d %s", w.Code, w.Body.String())
	}
}

func TestSystemLogDefaultLimitKeepsLastHundredPerSource(t *testing.T) {
	store := systemlog.New(500)
	for i := 0; i < 150; i++ {
		store.Add(systemlog.Info, "manager", fmt.Sprintf("m-%d", i))
		store.Add(systemlog.Info, "gateway", fmt.Sprintf("g-%d", i))
	}
	w := httptest.NewRecorder()
	NewSystemLogHandler(store).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/logs?scope=system", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	var body struct {
		Entries []systemlog.Entry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, entry := range body.Entries {
		counts[entry.Source]++
	}
	if len(body.Entries) != 200 || counts["manager"] != 100 || counts["gateway"] != 100 {
		t.Fatalf("entries=%d counts=%v first=%+v last=%+v", len(body.Entries), counts, body.Entries[0], body.Entries[len(body.Entries)-1])
	}
	if body.Entries[0].Message != "m-50" || body.Entries[1].Message != "g-50" || body.Entries[len(body.Entries)-1].Message != "g-149" {
		t.Fatalf("window first=%s %s last=%s", body.Entries[0].Message, body.Entries[1].Message, body.Entries[len(body.Entries)-1].Message)
	}
}

func TestSystemLogFiltersApplyBeforePerSourceLimit(t *testing.T) {
	store := systemlog.New(200)
	store.Add(systemlog.Error, "manager", "old failure")
	for i := 0; i < 100; i++ {
		store.Add(systemlog.Info, "manager", fmt.Sprintf("info-%d", i))
	}

	w := httptest.NewRecorder()
	NewSystemLogHandler(store).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/logs?scope=system&level=ERROR&limit=100", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "old failure") {
		t.Fatalf("json snapshot=%d %s", w.Code, w.Body.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream?scope=system&level=ERROR&limit=100", nil).WithContext(ctx)
	NewSystemLogHandler(store).ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "old failure") {
		t.Fatalf("stream snapshot=%d %s", w.Code, w.Body.String())
	}
}

func TestSystemLogAPIExposesInstanceSlugFromWorker(t *testing.T) {
	const instanceID = "8c821aec-1f0d-4b8d-a332-41c582dd2c58"
	const instanceSlug = "qwen-coder-32b"

	systemlog.Default.Reset()
	defer systemlog.Default.Reset()
	resourceid.RememberInstanceSlug(instanceID, instanceSlug)
	defer resourceid.ForgetInstanceSlug(instanceID)

	s := supervisor.New(fakeAPILogServer(t), "127.0.0.1", 33710, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	rt, err := s.StartWithEnv(ctx, instanceID, "model-1", "/tmp/model.gguf", nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background(), instanceID) }()
	if rt.InstanceID != instanceID {
		t.Fatalf("runtime instance id=%q want durable id %q", rt.InstanceID, instanceID)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		found := false
		for _, entry := range systemlog.Default.Snapshot(50) {
			if entry.Source == instanceSlug && strings.Contains(entry.Message, "fake api worker online") {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	w := httptest.NewRecorder()
	NewSystemLogHandler(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/logs?scope=system&source="+instanceSlug+"&limit=100", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"source":"`+instanceSlug+`"`) || !strings.Contains(w.Body.String(), "fake api worker online") || strings.Contains(w.Body.String(), `"source":"`+instanceID+`"`) {
		t.Fatalf("snapshot=%d %s", w.Code, w.Body.String())
	}

	streamCtx, streamCancel := context.WithCancel(context.Background())
	streamCancel()
	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream?scope=system&source="+instanceSlug+"&limit=100", nil).WithContext(streamCtx)
	NewSystemLogHandler(nil).ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "event: snapshot") || !strings.Contains(w.Body.String(), `"source":"`+instanceSlug+`"`) || !strings.Contains(w.Body.String(), "fake api worker online") || strings.Contains(w.Body.String(), `"source":"`+instanceID+`"`) {
		t.Fatalf("stream=%d %s", w.Code, w.Body.String())
	}
}
