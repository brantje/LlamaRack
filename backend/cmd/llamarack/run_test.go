package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/config"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func fakeDiscoveryBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llama-server")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "llama.cpp integration-test"
  exit 0
fi
if [ "$1" = "--help" ]; then
  echo "  --ctx-size N          context size"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func testConfig(t *testing.T, llamaPath string) config.Config {
	t.Helper()
	root := t.TempDir()
	return config.Config{
		ListenAddr:      "127.0.0.1:" + strconv.Itoa(freePort(t)),
		DataDir:         filepath.Join(root, "config"),
		ModelsDir:       filepath.Join(root, "models"),
		DatabasePath:    filepath.Join(root, "config", "manager.db"),
		LlamaServerPath: llamaPath,
		WorkerHost:      "127.0.0.1",
		WorkerPortStart: 35000,
		StartupTimeout:  time.Second,
		SessionLifetime: time.Hour,
		AllowedOrigin:   "http://localhost:3000",
	}
}

func testHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 2 * time.Second}
}

func waitHealthy(t *testing.T, client *http.Client, base string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get(base + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not become healthy")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitRunStopped(t *testing.T, done <-chan error, label string) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not shut down", label)
	}
}

func TestRunStartsEndpointsAndShutsDown(t *testing.T) {
	frontendDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(frontendDir, "index.html"), []byte("<html>integration frontend</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMARACK_FRONTEND_DIR", frontendDir)

	cfg := testConfig(t, fakeDiscoveryBinary(t))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()
	client := testHTTPClient(t)
	base := "http://" + cfg.ListenAddr
	waitHealthy(t, client, base)
	info, err := os.Stat(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("data dir mode=%04o want=0700", got)
	}
	dbInfo, err := os.Stat(cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := dbInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("database mode=%04o want=0600", got)
	}
	for _, path := range []string{"/health", "/", "/api/v1/health"} {
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s status=%d", path, resp.StatusCode)
		}
	}
	cancel()
	waitRunStopped(t, done, "run")
}

func TestRunStartsWhenLlamaDiscoveryUnavailable(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(t, filepath.Join(root, "missing-llama-server"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()
	client := testHTTPClient(t)
	waitHealthy(t, client, "http://"+cfg.ListenAddr)
	cancel()
	waitRunStopped(t, done, "run without discovery")
}

func TestRunErrors(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{ModelsDir: filepath.Join(blocker, "models")}
	if err := run(context.Background(), cfg); err == nil {
		t.Fatal("expected models directory error")
	}

	root = t.TempDir()
	port := freePort(t)
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg = config.Config{
		ListenAddr:      "127.0.0.1:" + strconv.Itoa(port),
		DataDir:         filepath.Join(root, "config"),
		ModelsDir:       filepath.Join(root, "models"),
		DatabasePath:    filepath.Join(root, "config", "manager.db"),
		LlamaServerPath: fakeDiscoveryBinary(t),
		WorkerHost:      "127.0.0.1", WorkerPortStart: 36000,
		StartupTimeout: time.Second, SessionLifetime: time.Hour,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := run(ctx, cfg); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestCheckHealth(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer ok.Close()
	if err := checkHealth(ok.URL); err != nil {
		t.Fatal(err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	if err := checkHealth(bad.URL); err == nil {
		t.Fatal("expected non-2xx error")
	}
	if err := checkHealth("http://127.0.0.1:1"); err == nil {
		t.Fatal("expected connection error")
	}
}
