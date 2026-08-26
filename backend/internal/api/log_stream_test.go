package api

import (
	"context"
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

	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
)

func fakeAPILogServer(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LCM_API_LOG_TEST_BINARY", exe)
	t.Setenv("GO_WANT_API_LOG_HELPER", "1")
	path := filepath.Join(t.TempDir(), "fake-llama-server")
	script := "#!/bin/sh\nexec \"$LCM_API_LOG_TEST_BINARY\" -test.run=TestAPILogHelperProcess -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAPILogHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_API_LOG_HELPER") != "1" {
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
	var port int
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--port" {
			port, _ = strconv.Atoi(args[i+1])
		}
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println("fake api worker online")
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	if err := (&http.Server{Handler: mux}).Serve(ln); err != nil && err != http.ErrServerClosed {
		os.Exit(3)
	}
}

func TestWorkerLogStreamRequiresAuthAndStreamsLiveWorkerOutput(t *testing.T) {
	f := newAPIFixture(t, nil)
	unauthorized := doRequest(t, f.server, http.MethodGet, "/api/v1/instances/instance-1/logs/stream", nil, nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	cookie := bootstrapAndLogin(t, f, "admin")
	model := createModel(t, f, cookie)
	instances, err := f.models.Instances(context.Background(), model.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%v err=%v", instances, err)
	}
	instanceID := instances[0].ID

	sup := supervisor.New(fakeAPILogServer(t), "127.0.0.1", 33500, 5*time.Second)
	f.server.lifecycle = lifecycle.New(f.models, sup)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		sup.Shutdown(ctx)
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+instanceID+"/logs/stream", nil).WithContext(ctx)
	req.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		f.server.ServeHTTP(recorder, req)
		close(done)
	}()

	time.Sleep(25 * time.Millisecond)
	started := doRequest(t, f.server, http.MethodPost, "/api/v1/models/"+model.ID+"/start", nil, cookie)
	if started.Code != http.StatusAccepted {
		cancel()
		t.Fatalf("start status=%d body=%s", started.Code, started.Body.String())
	}

	deadline := time.Now().Add(time.Second)
	for !strings.Contains(recorder.Body.String(), "fake api worker online") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("log stream did not close after request cancellation")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type=%q", got)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, ": connected") || !strings.Contains(body, "fake api worker online") {
		t.Fatalf("stream body=%q", body)
	}
}
