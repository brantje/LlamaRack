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
  -c,    --ctx-size N          context size
          --flash-attn [on|off|auto]  flash attention mode
          --host HOST          bind host
          --mmap, --no-mmap    whether to memory-map model
  -x                    ignored short option
  --invalid
  --gpu-layers N        GPU layers to offload
  prose mentioning --not-an-option should be ignored
`
	opts := parseHelp(help)
	if len(opts) != 7 {
		t.Fatalf("options=%+v", opts)
	}
	if opts[0].Key != "ctx-size" || opts[0].ValueHint != "N" || opts[0].Description != "context size" || opts[0].Kind != "integer" {
		t.Fatalf("ctx option=%+v", opts[0])
	}
	if opts[1].Key != "flash-attn" || opts[1].Kind != "enum" || len(opts[1].Choices) != 3 || opts[1].Choices[2] != "auto" {
		t.Fatalf("flash option=%+v", opts[1])
	}
	if opts[2].Key != "host" || !opts[2].ManagerOwned {
		t.Fatalf("manager-owned host metadata missing: %+v", opts[2])
	}
	if opts[3].Key != "mmap" || opts[4].Key != "no-mmap" || opts[3].Kind != "boolean" || opts[4].Kind != "boolean" {
		t.Fatalf("paired boolean options not discovered: %+v", opts[3:5])
	}
	if opts[5].Key != "invalid" || opts[5].Kind != "boolean" {
		t.Fatalf("flag option=%+v", opts[5])
	}
}

func TestClassifyValueHint(t *testing.T) {
	for _, tc := range []struct {
		hint string
		kind string
	}{
		{"", "boolean"}, {"N", "integer"}, {"INT", "integer"}, {"FLOAT", "number"},
		{"BOOL", "boolean"}, {"STRING", "string"}, {"N,N,...", "string"}, {"<cpu|gpu>", "enum"},
	} {
		kind, _ := classifyValueHint(tc.hint)
		if kind != tc.kind {
			t.Fatalf("hint %q kind=%q want=%q", tc.hint, kind, tc.kind)
		}
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
  printf '  -c, --ctx-size N      context size\n      --host HOST        bind host\n      --mmap, --no-mmap  mmap\n      --flash-attn       enable flash attention\n'
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
	if p.Path != path || p.Version != "llama.cpp test-version" || p.Fingerprint == "" || len(p.Options) != 5 {
		t.Fatalf("profile=%+v", p)
	}
	if p.Options[0].Kind != "integer" || !p.Options[1].ManagerOwned || p.Options[2].Key != "mmap" || p.Options[3].Key != "no-mmap" || p.Options[4].Kind != "boolean" {
		t.Fatalf("typed/owned options=%+v", p.Options)
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
