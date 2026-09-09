package benchmark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
)

func TestContextDepthWorkloadNormalizationAndContextBounds(t *testing.T) {
	workload, err := NormalizeWorkload(&WorkloadProfile{
		Version:          WorkloadSchemaVersion,
		PromptTokens:     []int{256},
		GenerationTokens: []int{128},
		ContextDepths:    []int{0, 2048, 2048},
		Repetitions:      2,
		Warmup:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(workload.ContextDepths) != 2 || workload.ContextDepths[0] != 0 || workload.ContextDepths[1] != 2048 {
		t.Fatalf("depths=%v", workload.ContextDepths)
	}
	if err := ValidateWorkloadContext(workload, InstanceConfigSnapshot{Options: map[string]string{"ctx-size": "4096"}}); err != nil {
		t.Fatal(err)
	}
	workload.ContextDepths = []int{4000}
	if err := ValidateWorkloadContext(workload, InstanceConfigSnapshot{Options: map[string]string{"ctx-size": "4096"}}); !errors.Is(err, ErrInvalidWorkload) {
		t.Fatalf("expected depth+case context failure, got %v", err)
	}

	if _, err := NormalizeWorkload(&WorkloadProfile{
		Version: LegacyWorkloadSchemaVersion, PromptTokens: []int{1}, ContextDepths: []int{1}, Repetitions: 1, Warmup: true,
	}); !errors.Is(err, ErrInvalidWorkload) {
		t.Fatalf("legacy depth err=%v", err)
	}
}

func TestBuildArgvContextDepthRequiresCapability(t *testing.T) {
	workload := WorkloadProfile{
		Version: WorkloadSchemaVersion, PromptTokens: []int{256}, GenerationTokens: []int{64}, ContextDepths: []int{0, 2048}, Repetitions: 2, Warmup: true,
	}
	caps := testCapabilities(llamacpp.Option{Key: "n-depth", Kind: "integer"})
	argv, err := BuildArgv("bench", "model.gguf", MappedConfig{}, workload, caps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(argv, " "), "--n-depth 0,2048") {
		t.Fatalf("argv=%v", argv)
	}
	if _, err := BuildArgv("bench", "model.gguf", MappedConfig{}, workload, testCapabilities()); !errors.Is(err, ErrInvalidWorkload) {
		t.Fatalf("missing capability err=%v", err)
	}
}

func TestParseMachineOutputIdentifiesContextDepth(t *testing.T) {
	results, err := ParseMachineOutput([]byte(`[
		{"n_prompt":0,"n_gen":128,"n_depth":0,"avg_ts":50},
		{"n_prompt":0,"n_gen":128,"n_depth":4096,"avg_ts":42}
	]`), WorkloadProfile{Repetitions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].CaseID != "tg-128" || results[1].CaseID != "tg-128-d4096" || results[1].ContextDepth != 4096 {
		t.Fatalf("results=%+v", results)
	}
}

func TestDiscoverCapabilitiesOnlyAdvertisesDepthWhenSupported(t *testing.T) {
	writeBench := func(name string, withDepth bool) string {
		path := filepath.Join(t.TempDir(), name)
		depth := ""
		if withDepth {
			depth = "--n-depth <n>  depth\n"
		}
		script := "#!/bin/sh\ncase \"$1\" in\n" +
			"  --version) echo test ;;\n" +
			"  --help) printf '%s' '--model <FNAME>  model\n--output <json>  output\n--repetitions <n>  repetitions\n--n-prompt <n>  prompt\n--n-gen <n>  generation\n" + depth + "' ;;\n" +
			"  --list-devices) echo 'Available devices:' ;;\n" +
			"esac\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}

	for _, tc := range []struct {
		name      string
		withDepth bool
		wantField bool
	}{{"old", false, false}, {"depth", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			caps, err := DiscoverCapabilities(context.Background(), writeBench(tc.name, tc.withDepth))
			if err != nil || !caps.Available {
				t.Fatalf("caps=%+v err=%v", caps, err)
			}
			found := false
			for _, field := range caps.Workload.Fields {
				found = found || field.Key == "context_depths"
			}
			if found != tc.wantField {
				t.Fatalf("context depth field=%v want %v", found, tc.wantField)
			}
		})
	}
}
