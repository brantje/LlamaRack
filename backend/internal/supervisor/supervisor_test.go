package supervisor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fakeServerScript(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMARACK_TEST_BINARY", exe)
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	path := filepath.Join(t.TempDir(), "fake-llama-server")
	script := "#!/bin/sh\nexec \"$LLAMARACK_TEST_BINARY\" -test.run=TestHelperProcess -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	idx := 0
	for i, arg := range args {
		if arg == "--" {
			idx = i + 1
			break
		}
	}
	args = args[idx:]
	var port int
	var model string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--port":
			port, _ = strconv.Atoi(args[i+1])
		case "--model":
			model = args[i+1]
		}
	}
	if strings.Contains(model, "exit-immediately") {
		fmt.Fprintln(os.Stderr, "intentional worker failure")
		os.Exit(2)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	fmt.Println("fake worker online")
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, r.Body)
	})
	server := &http.Server{Handler: mux}
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestStartReadyEndpointLogsAndStop(t *testing.T) {
	binary := fakeServerScript(t)
	s := New(binary, "127.0.0.1", 22000, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	rt, err := s.Start(ctx, "instance-1", "model-1", "/tmp/model.gguf", []string{"--ctx-size", "1024"})
	if err != nil {
		t.Fatal(err)
	}
	if rt.State != Ready || rt.PID == 0 || rt.Port < 22000 || rt.ReadyAt.IsZero() {
		t.Fatalf("runtime=%+v", rt)
	}
	endpoint, ok := s.Endpoint("instance-1")
	if !ok || !strings.Contains(endpoint, strconv.Itoa(rt.Port)) {
		t.Fatalf("endpoint=%q ok=%v", endpoint, ok)
	}

	second, err := s.Start(ctx, "instance-1", "model-1", "/tmp/model.gguf", nil)
	if err != nil || second.PID != rt.PID {
		t.Fatalf("second start=%+v err=%v", second, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(s.Logs("instance-1")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	logs := s.Logs("instance-1")
	joined := strings.Join(logs, "\n")
	if len(logs) == 0 || !strings.Contains(joined, "fake worker online") {
		t.Fatalf("logs=%v", logs)
	}
	for _, want := range []string{
		"launch command:",
		strconv.Quote(binary),
		`"--model" "/tmp/model.gguf"`,
		`"--host" "127.0.0.1"`,
		`"--port" "` + strconv.Itoa(rt.Port) + `"`,
		`"--ctx-size" "1024"`,
		`"--cors-origins" "localhost"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("launch command missing %q in logs=%v", want, logs)
		}
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx, "instance-1"); err != nil {
		t.Fatal(err)
	}
	if got := s.Status("instance-1"); got.State != Unloaded || got.PID != 0 {
		t.Fatalf("stopped runtime=%+v", got)
	}
	if _, ok := s.Endpoint("instance-1"); ok {
		t.Fatal("stopped worker should have no endpoint")
	}
	if err := s.Stop(context.Background(), "missing"); err != nil {
		t.Fatalf("stopping missing worker: %v", err)
	}
}

func TestBeginDrainClosesEndpointBeforeStop(t *testing.T) {
	binary := fakeServerScript(t)
	s := New(binary, "127.0.0.1", 22100, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		s.Shutdown(stopCtx)
	})

	if s.BeginDrain("missing") {
		t.Fatal("BeginDrain on missing worker should fail")
	}
	rt, err := s.Start(ctx, "drain-me", "model-1", "/tmp/model.gguf", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.BeginDrain("drain-me") {
		t.Fatal("BeginDrain on READY worker should succeed")
	}
	if s.BeginDrain("drain-me") {
		t.Fatal("BeginDrain on DRAINING worker should fail")
	}
	if got := s.Status("drain-me"); got.State != Draining {
		t.Fatalf("state=%s", got.State)
	}
	if _, ok := s.Endpoint("drain-me"); ok {
		t.Fatal("DRAINING worker must not expose an endpoint")
	}
	if _, err := s.Start(ctx, "drain-me", "model-1", "/tmp/model.gguf", nil); err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("start during drain err=%v", err)
	}
	if !s.AbortDrain("drain-me") {
		t.Fatal("AbortDrain should restore READY")
	}
	if got := s.Status("drain-me"); got.State != Ready || got.PID != rt.PID {
		t.Fatalf("aborted drain runtime=%+v", got)
	}
	if !s.BeginDrain("drain-me") {
		t.Fatal("BeginDrain after abort should succeed")
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := s.Stop(stopCtx, "drain-me"); err != nil {
		t.Fatal(err)
	}
	if got := s.Status("drain-me"); got.State != Unloaded {
		t.Fatalf("stopped after drain=%+v", got)
	}
	if s.AbortDrain("drain-me") {
		t.Fatal("AbortDrain after stop should fail")
	}
	if err := s.WaitInactive(ctx, "drain-me"); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitInactive(ctx, "never-started"); err != nil {
		t.Fatal(err)
	}
}

func TestStartFailuresAndStatusDefaults(t *testing.T) {
	missing := New(filepath.Join(t.TempDir(), "missing-binary"), "127.0.0.1", 24000, 100*time.Millisecond)
	if _, err := missing.Start(context.Background(), "bad", "model", "/tmp/model.gguf", nil); err == nil {
		t.Fatal("expected missing binary error")
	}
	if got := missing.Status("bad"); got.State != Failed || got.LastError == "" {
		t.Fatalf("failed status=%+v", got)
	}
	if got := missing.Status("unknown"); got.State != Unloaded || got.InstanceID != "unknown" {
		t.Fatalf("unknown status=%+v", got)
	}
	if logs := missing.Logs("unknown"); logs != nil {
		t.Fatalf("unknown logs=%v", logs)
	}

	binary := fakeServerScript(t)
	s := New(binary, "127.0.0.1", 25000, 150*time.Millisecond)
	_, err := s.Start(context.Background(), "exit", "model", "/tmp/exit-immediately.gguf", nil)
	if err == nil {
		t.Fatal("expected readiness failure")
	}
	if got := s.Status("exit"); got.State != Failed || got.LastError == "" {
		t.Fatalf("failed readiness status=%+v", got)
	}
	if err := s.Stop(context.Background(), "exit"); err != nil {
		t.Fatalf("stopping exited worker: %v", err)
	}
	if got := s.Status("exit"); got.State != Failed {
		t.Fatalf("stopping exited worker changed state=%+v", got)
	}
}

func TestRingCopyLogsPortAllocationAndShutdown(t *testing.T) {
	r := newRing(2)
	r.add("one")
	r.add("two")
	r.add("three")
	if got := r.lines(); len(got) != 2 || got[0] != "two" || got[1] != "three" {
		t.Fatalf("ring=%v", got)
	}
	copyLogs(r, "instance", "model", "stderr", strings.NewReader("a\nb\n"))
	got := r.lines()
	if len(got) != 2 {
		t.Fatalf("copied logs=%v", got)
	}
	assertTimestampedLog(t, got[0], "stderr", "a")
	assertTimestampedLog(t, got[1], "stderr", "b")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	occupied := ln.Addr().(*net.TCPAddr).Port
	defer ln.Close()
	s := New("unused", "127.0.0.1", occupied, time.Second)
	s.mu.Lock()
	port, err := s.allocatePortLocked()
	s.mu.Unlock()
	if err != nil || port == occupied {
		t.Fatalf("allocated port=%d err=%v occupied=%d", port, err, occupied)
	}

	binary := fakeServerScript(t)
	running := New(binary, "127.0.0.1", 27000, 5*time.Second)
	if _, err := running.Start(context.Background(), "a", "m", "/tmp/a.gguf", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := running.Start(context.Background(), "b", "m", "/tmp/b.gguf", nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	running.Shutdown(ctx)
	if running.Status("a").State != Unloaded || running.Status("b").State != Unloaded {
		t.Fatalf("shutdown states: a=%+v b=%+v", running.Status("a"), running.Status("b"))
	}
}
