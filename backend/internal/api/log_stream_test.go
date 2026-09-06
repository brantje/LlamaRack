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

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func fakeAPILogServer(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMARACK_API_LOG_TEST_BINARY", exe)
	t.Setenv("GO_WANT_API_LOG_HELPER", "1")
	path := filepath.Join(t.TempDir(), "fake-llama-server")
	script := "#!/bin/sh\nexec \"$LLAMARACK_API_LOG_TEST_BINARY\" -test.run=TestAPILogHelperProcess -- \"$@\"\n"
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

func TestWorkerLogStreamStreamsLiveWorkerOutput(t *testing.T) {
	f := newAPIFixture(t, nil)
	model := createModel(t, f, nil)
	instance, err := f.server.lifecycle.Instances().Create(context.Background(), instances.CreateInput{ModelID: model.ID, Name: "API log instance"})
	if err != nil {
		t.Fatal(err)
	}
	instanceID := instance.ID
	instanceSlug := instance.Slug

	sup := supervisor.New(fakeAPILogServer(t), "127.0.0.1", 33500, 5*time.Second)
	f.server.lifecycle = lifecycle.New(f.models, sup)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		sup.Shutdown(ctx)
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/instances/"+instanceSlug+"/logs/stream", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { f.server.ServeHTTP(recorder, req); close(done) }()

	time.Sleep(25 * time.Millisecond)
	started := doRequest(t, f.server, http.MethodPost, "/api/v1/instances/"+instanceSlug+"/start", nil, nil)
	if started.Code != http.StatusAccepted {
		cancel()
		t.Fatalf("start status=%d body=%s", started.Code, started.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(strings.Join(sup.Logs(instanceID), "\n"), "fake api worker online") {
			break
		}
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
