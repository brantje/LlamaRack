package llamacpp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
if [ "$1" = "--list-devices" ]; then
  printf 'Available devices:\n'
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

func TestParseListDevices(t *testing.T) {
	devices, err := parseListDevices("ggml_cuda_init: found 2 CUDA devices:\nAvailable devices:\n  CUDA0: NVIDIA A (100 MiB, 90 MiB free)\nCUDA1: NVIDIA B (100 MiB, 80 MiB free)\n  CUDA0: duplicate\n")
	if err != nil || strings.Join(devices, ",") != "CUDA0,CUDA1" {
		t.Fatalf("devices=%v err=%v", devices, err)
	}
	devices, err = parseListDevices("Available devices:\n")
	if err != nil || len(devices) != 0 {
		t.Fatalf("cpu-only devices=%v err=%v", devices, err)
	}
	if _, err := parseListDevices("CUDA0: device without marker\n"); err == nil {
		t.Fatal("expected missing marker error")
	}
}

func TestDiscoverDevicesAndFingerprint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is unix-specific")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "llama-server")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'llama.cpp test-version\n'; exit 0; fi
if [ "$1" = "--help" ]; then printf '  -c, --ctx-size N      context size\n'; exit 0; fi
if [ "$1" = "--list-devices" ]; then
  printf 'backend init noise\nAvailable devices:\n  CUDA0: GPU (100 MiB, %s MiB free)\n' "${LLAMACPP_TEST_FREE:-90}"
  if [ "$LLAMACPP_TEST_SECOND" = "1" ]; then printf '  Vulkan0: GPU (200 MiB, 150 MiB free)\n'; fi
  exit 0
fi
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LLAMACPP_TEST_FREE", "90")
	p, err := Discover(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !p.DeviceDiscoveryAvailable || strings.Join(p.Devices, ",") != "CUDA0" || p.DeviceDiscoveryError != "" {
		t.Fatalf("profile=%+v", p)
	}
	t.Setenv("LLAMACPP_TEST_FREE", "10")
	p2, err := Discover(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Fingerprint != p.Fingerprint {
		t.Fatalf("free-memory noise changed fingerprint: %q != %q", p2.Fingerprint, p.Fingerprint)
	}
	t.Setenv("LLAMACPP_TEST_SECOND", "1")
	p3, err := Discover(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if p3.Fingerprint == p.Fingerprint || strings.Join(p3.Devices, ",") != "CUDA0,Vulkan0" {
		t.Fatalf("device set did not change fingerprint/profile: %+v", p3)
	}
}

func TestDiscoverDeviceFailureIsNonFatal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is unix-specific")
	}
	path := filepath.Join(t.TempDir(), "llama-server")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'version\n'; exit 0; fi
if [ "$1" = "--help" ]; then printf '  --ctx-size N  context\n'; exit 0; fi
if [ "$1" = "--list-devices" ]; then printf 'probe failed\n' >&2; exit 3; fi
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Discover(context.Background(), path)
	if err != nil {
		t.Fatalf("device discovery must not discard option profile: %v", err)
	}
	if p.DeviceDiscoveryAvailable || p.DeviceDiscoveryError == "" || len(p.Devices) != 0 {
		t.Fatalf("profile=%+v", p)
	}
}

func TestDiscoverCPUOnlyDeviceSet(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is unix-specific")
	}
	path := filepath.Join(t.TempDir(), "llama-server")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'version\n'; exit 0; fi
if [ "$1" = "--help" ]; then printf '  --ctx-size N  context\n'; exit 0; fi
if [ "$1" = "--list-devices" ]; then printf 'Available devices:\n'; exit 0; fi
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Discover(context.Background(), path)
	if err != nil || !p.DeviceDiscoveryAvailable || len(p.Devices) != 0 {
		t.Fatalf("profile=%+v err=%v", p, err)
	}
}
