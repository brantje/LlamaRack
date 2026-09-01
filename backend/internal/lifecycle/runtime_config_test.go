package lifecycle

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func lifecycleCaptureBinary(t *testing.T) (string, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMARACK_LIFECYCLE_TEST_BINARY", exe)
	t.Setenv("GO_WANT_LIFECYCLE_HELPER", "1")
	argsFile := filepath.Join(t.TempDir(), "worker-args.txt")
	t.Setenv("LLAMARACK_LIFECYCLE_CAPTURE_ARGS", argsFile)
	path := filepath.Join(t.TempDir(), "fake-llama-capture")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LLAMARACK_LIFECYCLE_CAPTURE_ARGS\"\nexec \"$LLAMARACK_LIFECYCLE_TEST_BINARY\" -test.run=TestLifecycleHelperProcess -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, argsFile
}

func freeLifecyclePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestResolvedRuntimeConfigReachesWorkerArgv(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, ms, m, _, exec := setupLifecycle(t, true, false)
	instances, err := ms.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	instanceID := instances[0].ID

	// Remove fixture overrides so the manager runtime default is the source of
	// ctx-size, then add an explicit false override with a discovered inverse.
	exec("DELETE FROM model_options WHERE model_id=?", m.ID)
	exec("INSERT INTO instance_options(instance_id,option_key,option_value) VALUES(?,?,?)", instanceID, "flash-attn", "false")

	binary, argsFile := lifecycleCaptureBinary(t)
	sup := supervisor.New(binary, "127.0.0.1", freeLifecyclePort(t), 5*time.Second)
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		sup.Shutdown(stopCtx)
	})

	s := New(ms, sup)
	s.SetProfileGetter(func() (llamacpp.Profile, error) {
		return llamacpp.Profile{Version: "test", Options: []llamacpp.Option{
			{Key: "ctx-size", Kind: "integer"},
			{Key: "flash-attn", Kind: "boolean"},
			{Key: "no-flash-attn", Kind: "boolean"},
		}}, nil
	})

	if _, err := s.StartInstance(ctx, instanceID); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "--ctx-size\n4096\n") {
		t.Fatalf("manager context default missing from worker argv: %q", raw)
	}
	if !strings.Contains(text, "--no-flash-attn\n") {
		t.Fatalf("explicit false did not reach worker as inverse flag: %q", raw)
	}
	if strings.Contains(text, "--flash-attn\n") {
		t.Fatalf("positive boolean flag leaked into false launch: %q", raw)
	}
}
