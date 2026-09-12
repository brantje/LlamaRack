package llamacpp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestParseShortOptions(t *testing.T) {
	help := `
  -p, --n-prompt <n>      prompt
  -pg <pp,tg>             combined prompt + generation
  -d, --n-depth <n>       depth
      --long-only <n>     long only
  prose mentioning -x should be ignored
`
	if got, want := parseShortOptions(help), []string{"d", "p", "pg"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("short options=%v want %v", got, want)
	}
}

func TestDiscoverRetainsShortOnlyOptions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is unix-specific")
	}
	path := filepath.Join(t.TempDir(), "llama-bench")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'test\n'; exit 0; fi
if [ "$1" = "--help" ]; then
  printf '%s\n' '-p, --n-prompt <n>  prompt' '-n, --n-gen <n>  generation' '-pg <pp,tg>  combined'
  exit 0
fi
if [ "$1" = "--list-devices" ]; then printf 'Available devices:\n'; exit 0; fi
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	profile, err := Discover(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !profile.HasShort("pg") || !profile.HasShort("-pg") || profile.HasShort("missing") {
		t.Fatalf("profile short options=%v", profile.ShortOptions)
	}
}
