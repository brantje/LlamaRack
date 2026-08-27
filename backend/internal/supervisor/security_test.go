package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func secureArgsServerScript(t *testing.T) (string, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LCM_TEST_BINARY", exe)
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	argsFile := filepath.Join(t.TempDir(), "worker-args.txt")
	t.Setenv("LCM_CAPTURE_ARGS", argsFile)
	path := filepath.Join(t.TempDir(), "fake-llama-server-capture")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LCM_CAPTURE_ARGS\"\nexec \"$LCM_TEST_BINARY\" -test.run=TestHelperProcess -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, argsFile
}

func TestSanitizeWorkerSecurityArgs(t *testing.T) {
	got := sanitizeWorkerSecurityArgs([]string{
		"--ctx-size", "1024",
		"--cors-origins", "*",
		"--api-key", "worker-secret",
		"--api-key-file=/tmp/key",
		"--cors-headers=X-Test",
		"--cors-credentials",
	})
	want := []string{"--ctx-size", "1024"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitizeWorkerSecurityArgs() = %v, want %v", got, want)
	}
}

func TestStartEnforcesSecureCORSAfterProvidedArgs(t *testing.T) {
	binary, argsFile := secureArgsServerScript(t)
	s := New(binary, "127.0.0.1", 29000, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	if _, err := s.Start(ctx, "secure-instance", "model-1", "/tmp/model.gguf", []string{
		"--ctx-size", "1024",
		"--cors-origins", "*",
		"--api-key", "worker-secret",
	}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background(), "secure-instance") }()

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(raw))
	if len(args) < 2 || args[len(args)-2] != "--cors-origins" || args[len(args)-1] != "localhost" {
		t.Fatalf("worker args do not end with manager-owned secure CORS policy: %v", args)
	}
	if strings.Count(string(raw), "--cors-origins\n") != 1 {
		t.Fatalf("worker args should contain exactly one CORS policy: %q", raw)
	}
	if strings.Contains(string(raw), "worker-secret") || strings.Contains(string(raw), "--api-key") {
		t.Fatalf("stale worker credentials must not reach process arguments: %q", raw)
	}
	if !strings.Contains(string(raw), "--ctx-size\n1024") {
		t.Fatalf("ordinary llama.cpp overrides were not preserved: %q", raw)
	}
}
