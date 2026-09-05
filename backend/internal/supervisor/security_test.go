package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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
	t.Setenv("LLAMARACK_TEST_BINARY", exe)
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	argsFile := filepath.Join(t.TempDir(), "worker-args.txt")
	t.Setenv("LLAMARACK_CAPTURE_ARGS", argsFile)
	path := filepath.Join(t.TempDir(), "fake-llama-server-capture")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LLAMARACK_CAPTURE_ARGS\"\nexec \"$LLAMARACK_TEST_BINARY\" -test.run=TestHelperProcess -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, argsFile
}

func TestSanitizeWorkerOwnedArgs(t *testing.T) {
	got := sanitizeWorkerOwnedArgs([]string{
		"--ctx-size", "1024",
		"--model", "/tmp/other.gguf",
		"--alias", "user-alias",
		"--host", "0.0.0.0",
		"--port", "9999",
		"--cors-origins", "*",
		"--api-key", "worker-secret",
		"--api-key-file=/tmp/key",
		"--cors-headers=X-Test",
		"--cors-credentials",
		"--no-slots",
		"--slot-save-path", "/tmp/escape",
	})
	want := []string{"--ctx-size", "1024"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sanitizeWorkerOwnedArgs() = %v, want %v", got, want)
	}
}

func TestStartEnforcesSecureManagerOwnedArgs(t *testing.T) {
	binary, argsFile := secureArgsServerScript(t)
	s := New(binary, "127.0.0.1", 29000, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	rt, err := s.Start(ctx, "secure-instance", "model-1", "/tmp/model.gguf", []string{
		"--ctx-size", "1024",
		"--model", "/tmp/other.gguf",
		"--host", "0.0.0.0",
		"--port", "9999",
		"--cors-origins", "*",
		"--api-key", "worker-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background(), "secure-instance") }()

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	args := strings.Fields(text)
	if len(args) < 2 || args[len(args)-2] != "--cors-origins" || args[len(args)-1] != "localhost" {
		t.Fatalf("worker args do not end with manager-owned secure CORS policy: %v", args)
	}
	for _, flag := range []string{"--model\n", "--alias\n", "--host\n", "--port\n", "--cors-origins\n"} {
		if strings.Count(text, flag) != 1 {
			t.Fatalf("worker args should contain exactly one %s setting: %q", strings.TrimSpace(flag), raw)
		}
	}
	if !strings.Contains(text, "--model\n/tmp/model.gguf") || !strings.Contains(text, "--alias\nsecure-instance") || !strings.Contains(text, "--host\n127.0.0.1") || !strings.Contains(text, "--port\n"+strconv.Itoa(rt.Port)) {
		t.Fatalf("manager-owned model/bind arguments were not preserved: %q", raw)
	}
	if strings.Contains(text, "/tmp/other.gguf\n") || strings.Contains(text, "user-alias\n") || strings.Contains(text, "0.0.0.0\n") || strings.Contains(text, "\n9999\n") {
		t.Fatalf("conflicting manager-owned arguments reached worker: %q", raw)
	}
	if strings.Contains(text, "worker-secret\n") || strings.Contains(text, "--api-key\n") {
		t.Fatalf("stale worker credentials must not reach process arguments: %q", raw)
	}
	if !strings.Contains(text, "--ctx-size\n1024") {
		t.Fatalf("ordinary llama.cpp overrides were not preserved: %q", raw)
	}
}
