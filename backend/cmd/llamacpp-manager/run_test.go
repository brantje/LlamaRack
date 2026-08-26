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

	"github.com/brantje/llamacpp-manager/backend/internal/config"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
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
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { t.Fatal(err) }
	return path
}

func TestRunStartsEndpointsAndShutsDown(t *testing.T) {
	root := t.TempDir()
	port := freePort(t)
	cfg := config.Config{
		ListenAddr: "127.0.0.1:" + strconv.Itoa(port),
		DataDir: filepath.Join(root, "config"),
		ModelsDir: filepath.Join(root, "models"),
		DatabasePath: filepath.Join(root, "config", "manager.db"),
		LlamaServerPath: fakeDiscoveryBinary(t),
		WorkerHost: "127.0.0.1",
		WorkerPortStart: 35000,
		StartupTimeout: time.Second,
		SessionLifetime: time.Hour,
		AllowedOrigin: "http://localhost:3000",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func(){ done <- run(ctx, cfg) }()
	base := "http://" + cfg.ListenAddr
	deadline := time.Now().Add(5*time.Second)
	for {
		resp, err := http.Get(base + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 { break }
		}
		if time.Now().After(deadline) { t.Fatal("server did not become healthy") }
		time.Sleep(20*time.Millisecond)
	}
	for _, path := range []string{"/health", "/", "/api/v1/health"} {
		resp, err := http.Get(base + path)
		if err != nil { t.Fatal(err) }
		_ = resp.Body.Close()
		if resp.StatusCode != 200 { t.Fatalf("GET %s status=%d", path, resp.StatusCode) }
	}
	cancel()
	select {
	case err := <-done:
		if err != nil { t.Fatalf("run: %v", err) }
	case <-time.After(3*time.Second):
		t.Fatal("run did not shut down")
	}
}

func TestRunErrors(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil { t.Fatal(err) }
	cfg := config.Config{ModelsDir: filepath.Join(blocker, "models")}
	if err := run(context.Background(), cfg); err == nil { t.Fatal("expected models directory error") }

	root = t.TempDir()
	port := freePort(t)
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil { t.Fatal(err) }
	defer ln.Close()
	cfg = config.Config{
		ListenAddr: "127.0.0.1:"+strconv.Itoa(port),
		DataDir: filepath.Join(root,"config"),
		ModelsDir: filepath.Join(root,"models"),
		DatabasePath: filepath.Join(root,"config","manager.db"),
		LlamaServerPath: fakeDiscoveryBinary(t),
		WorkerHost: "127.0.0.1", WorkerPortStart: 36000,
		StartupTimeout: time.Second, SessionLifetime: time.Hour,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := run(ctx, cfg); err == nil { t.Fatal("expected listen error") }
}

func TestCheckHealth(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request){ w.WriteHeader(204) }))
	defer ok.Close()
	if err := checkHealth(ok.URL); err != nil { t.Fatal(err) }
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request){ w.WriteHeader(503) }))
	defer bad.Close()
	if err := checkHealth(bad.URL); err == nil { t.Fatal("expected non-2xx error") }
	if err := checkHealth("http://127.0.0.1:1"); err == nil { t.Fatal("expected connection error") }
}
