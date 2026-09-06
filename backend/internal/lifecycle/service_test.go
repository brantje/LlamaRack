package lifecycle

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func lifecycleFakeBinary(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMARACK_LIFECYCLE_TEST_BINARY", exe)
	t.Setenv("GO_WANT_LIFECYCLE_HELPER", "1")
	path := filepath.Join(t.TempDir(), "fake-llama")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec \"$LLAMARACK_LIFECYCLE_TEST_BINARY\" -test.run=TestLifecycleHelperProcess -- \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLifecycleHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LIFECYCLE_HELPER") != "1" {
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
	readyDelay := time.Duration(0)
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--port":
			port, _ = strconv.Atoi(args[i+1])
		case "--test-ready-delay-ms":
			milliseconds, _ := strconv.Atoi(args[i+1])
			readyDelay = time.Duration(milliseconds) * time.Millisecond
		}
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		os.Exit(2)
	}
	readyAt := time.Now().Add(readyDelay)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if time.Now().Before(readyAt) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	server := &http.Server{Handler: mux}
	_ = server.Serve(ln)
	os.Exit(0)
}

func setupLifecycle(t *testing.T, autoload, alwaysOn bool) (*Service, *models.Service, models.Model, *supervisor.Supervisor, func(string, ...any)) {
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
	ms := models.New(db, modelsDir)
	modelPath := filepath.Join(modelsDir, "test-Q4_K_M.gguf")
	if err := os.WriteFile(modelPath, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := ms.Create(ctx, models.CreateModelInput{PublicID: "test-model", Name: "Test model", GGUFPath: modelPath, Autoload: &autoload, AlwaysOn: alwaysOn, Options: map[string]string{"ctx-size": "2048", "flash-attn": "true", "disabled": "false", "empty": ""}})
	if err != nil {
		t.Fatal(err)
	}
	createdInstances, err := ms.Instances(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(createdInstances) != 1 {
		t.Fatalf("expected one compatibility Instance, got %d", len(createdInstances))
	}
	// Lifecycle APIs are internal and keyed by the immutable Instance UUID. The
	// deprecated PublicID field is retained in these shared tests only as a
	// convenient fixture handle.
	m.PublicID = createdInstances[0].ID
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	portStart := probe.Addr().(*net.TCPAddr).Port
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New(lifecycleFakeBinary(t), "127.0.0.1", portStart, 5*time.Second)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sup.Shutdown(ctx)
	})
	s := New(ms, sup)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	return s, ms, m, sup, exec
}

func TestOptionArgs(t *testing.T) {
	got := optionArgs(map[string]string{"z": "3", "--alpha": "1", "flag": "TRUE", "off": "false", "blank": " "})
	want := []string{"--alpha", "1", "--flag", "--z", "3"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("optionArgs=%v want=%v", got, want)
	}
}

func TestEnsureReadyAutoloadStartStopRuntimeAndSingleFlight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, m, sup, _ := setupLifecycle(t, true, false)
	const callers = 20
	results := make(chan string, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ep, err := s.EnsureReady(ctx, m.PublicID)
			if err != nil {
				errs <- err
				return
			}
			results <- ep
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var endpoint string
	for ep := range results {
		if endpoint == "" {
			endpoint = ep
		}
		if ep != endpoint {
			t.Fatalf("different endpoints: %q vs %q", ep, endpoint)
		}
	}
	if endpoint == "" {
		t.Fatal("missing endpoint")
	}
	instances, err := s.Runtime(ctx, m.ID)
	if err != nil || len(instances) != 1 || instances[0].State != supervisor.Ready {
		t.Fatalf("runtime=%+v err=%v", instances, err)
	}
	second, err := s.StartModel(ctx, m.ID)
	if err != nil || second != endpoint {
		t.Fatalf("second start=%q err=%v", second, err)
	}
	modelInstances, _ := s.models.Instances(ctx, m.ID)
	if got := sup.Status(modelInstances[0].ID); got.State != supervisor.Ready {
		t.Fatalf("supervisor status=%+v", got)
	}
	if err := s.StopModel(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	instances, _ = s.Runtime(ctx, m.ID)
	if instances[0].State != supervisor.Unloaded {
		t.Fatalf("stopped runtime=%+v", instances)
	}
}

func TestEnsureReadyPoliciesAndMissingInstance(t *testing.T) {
	ctx := context.Background()
	s, _, m, _, exec := setupLifecycle(t, false, false)
	if _, err := s.EnsureReady(ctx, "missing"); err == nil {
		t.Fatal("expected missing instance error")
	}
	if _, err := s.EnsureReady(ctx, m.PublicID); err == nil || !strings.Contains(err.Error(), "autoload disabled") {
		t.Fatalf("expected autoload disabled error, got %v", err)
	}
	exec("UPDATE instances SET enabled=0 WHERE model_id=?", m.ID)
	if _, err := s.EnsureReady(ctx, m.PublicID); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
}

func TestNoEnabledInstanceAndAlwaysOnReconciliation(t *testing.T) {
	ctx := context.Background()
	s, ms, m, _, exec := setupLifecycle(t, true, false)
	exec("UPDATE instances SET enabled=0 WHERE model_id=?", m.ID)
	if _, err := s.StartModel(ctx, m.ID); err == nil || !strings.Contains(err.Error(), "no enabled instance") {
		t.Fatalf("expected no instance error, got %v", err)
	}

	s2, ms2, m2, sup2, _ := setupLifecycle(t, true, true)
	s2.ReconcileAlwaysOn(ctx)
	instances, err := ms2.Instances(ctx, m2.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) && sup2.Status(instances[0].ID).State != supervisor.Ready {
		time.Sleep(20 * time.Millisecond)
	}
	if got := sup2.Status(instances[0].ID); got.State != supervisor.Ready {
		t.Fatalf("always-on status=%+v", got)
	}
	if logs := s2.Logs(instances[0].ID); logs == nil {
	}
	_ = ms
}

func TestRunReconcilerStopsWithContext(t *testing.T) {
	for _, interval := range []time.Duration{0, 15 * time.Second} {
		t.Run(interval.String(), func(t *testing.T) {
			s, _, _, _, _ := setupLifecycle(t, false, false)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				s.RunReconciler(ctx, interval)
				close(done)
			}()
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("reconciler did not stop")
			}
		})
	}
}