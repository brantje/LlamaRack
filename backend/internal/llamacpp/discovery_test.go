package llamacpp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFirstLineAndParseHelp(t *testing.T) {
	if got := firstLine("  version 1\nsecond\n"); got != "version 1" {
		t.Fatalf("firstLine=%q", got)
	}
	if got := firstLine("   "); got != "" {
		t.Fatalf("blank firstLine=%q", got)
	}
	help := `
  --ctx-size N          context size
  --flash-attn          enable flash attention
  -x                    ignored short option
  --invalid
  --gpu-layers N        GPU layers to offload
`
	opts := parseHelp(help)
	if len(opts) != 3 {
		t.Fatalf("options=%+v", opts)
	}
	if opts[0].Key != "ctx-size" || opts[0].ValueHint != "N" || opts[0].Description != "context size" {
		t.Fatalf("ctx option=%+v", opts[0])
	}
	if opts[1].Key != "flash-attn" || opts[1].ValueHint != "" || opts[1].Description != "enable flash attention" {
		t.Fatalf("flag option=%+v", opts[1])
	}
}

func TestDiscover(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is unix-specific")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "llama-server")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf 'llama.cpp test-version\nextra\n'
  exit 0
fi
if [ "$1" = "--help" ]; then
  printf '  --ctx-size N          context size\n  --flash-attn          enable flash attention\n'
  exit 0
fi
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Discover(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != path || p.Version != "llama.cpp test-version" || p.Fingerprint == "" || len(p.Options) != 2 {
		t.Fatalf("profile=%+v", p)
	}
	p2, err := Discover(context.Background(), path)
	if err != nil || p2.Fingerprint != p.Fingerprint {
		t.Fatalf("fingerprint should be stable: %+v err=%v", p2, err)
	}
}

func TestDiscoverHelpFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is unix-specific")
	}
	path := filepath.Join(t.TempDir(), "broken")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(context.Background(), path); err == nil {
		t.Fatal("expected help failure")
	}
}
