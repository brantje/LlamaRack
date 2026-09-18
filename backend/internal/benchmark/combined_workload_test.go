package benchmark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBenchScript(t *testing.T, name, optional string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"  --version) echo test ;;\n" +
		"  --help) printf '%b' '--model <FNAME>  model\\n--output <json>  output\\n--repetitions <n>  repetitions\\n--n-prompt <n>  prompt\\n--n-gen <n>  generation\\n" + optional + "' ;;\n" +
		"  --list-devices) echo 'Available devices:' ;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSameWorkloadShapeComparesDimensions(t *testing.T) {
	preset, ok := workloadPreset(DefaultWorkloadID)
	if !ok {
		t.Fatal("missing default preset")
	}
	if !sameWorkloadShape(preset, preset) || sameWorkloadShape(preset, WorkloadProfile{Repetitions: preset.Repetitions, Warmup: preset.Warmup}) {
		t.Fatal("preset shape comparison")
	}
	changed := preset
	changed.CombinedCases = []WorkloadCombinedCase{{PromptTokens: 1, GenerationTokens: 1}}
	if sameWorkloadShape(preset, changed) {
		t.Fatal("combined case mismatch should fail")
	}
	shorter := preset
	shorter.PromptTokens = shorter.PromptTokens[:1]
	if sameWorkloadShape(preset, shorter) {
		t.Fatal("prompt token length mismatch should fail")
	}
}

func TestCombinedWorkloadNormalizationAndContextBounds(t *testing.T) {
	workload, err := NormalizeWorkload(&WorkloadProfile{
		Version: WorkloadSchemaVersion,
		CombinedCases: []WorkloadCombinedCase{
			{PromptTokens: 256, GenerationTokens: 64},
			{PromptTokens: 256, GenerationTokens: 64},
			{PromptTokens: 1024, GenerationTokens: 128},
		},
		ContextDepths: []int{0, 2048},
		Repetitions:   2,
		Warmup:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(workload.CombinedCases) != 2 || workload.CombinedCases[1].PromptTokens != 1024 {
		t.Fatalf("combined=%+v", workload.CombinedCases)
	}
	if err := ValidateWorkloadContext(workload, InstanceConfigSnapshot{Options: map[string]string{"ctx-size": "4096"}}); err != nil {
		t.Fatal(err)
	}
	workload.ContextDepths = []int{3000}
	if err := ValidateWorkloadContext(workload, InstanceConfigSnapshot{Options: map[string]string{"ctx-size": "4096"}}); !errors.Is(err, ErrInvalidWorkload) {
		t.Fatalf("expected combined depth bound error, got %v", err)
	}

	if _, err := NormalizeWorkload(&WorkloadProfile{
		Version: LegacyWorkloadSchemaVersion,
		CombinedCases: []WorkloadCombinedCase{{PromptTokens: 1, GenerationTokens: 1}},
		Repetitions: 1,
		Warmup: true,
	}); !errors.Is(err, ErrInvalidWorkload) {
		t.Fatalf("legacy combined err=%v", err)
	}
	if _, err := NormalizeWorkload(&WorkloadProfile{
		Version: WorkloadSchemaVersion,
		CombinedCases: []WorkloadCombinedCase{{PromptTokens: 0, GenerationTokens: 1}},
		Repetitions: 1,
		Warmup: true,
	}); !errors.Is(err, ErrInvalidWorkload) {
		t.Fatalf("invalid combined err=%v", err)
	}
}

func TestBuildArgvCombinedCasesRequiresShortPG(t *testing.T) {
	workload := WorkloadProfile{
		Version: WorkloadSchemaVersion,
		CombinedCases: []WorkloadCombinedCase{{PromptTokens: 256, GenerationTokens: 64}, {PromptTokens: 1024, GenerationTokens: 128}},
		Repetitions: 2,
		Warmup: true,
	}
	caps := testCapabilities()
	caps.profile.ShortOptions = []string{"pg"}
	argv, err := BuildArgv("bench", "model.gguf", MappedConfig{}, workload, caps)
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(argv, " ")
	for _, expected := range []string{"--n-prompt 0", "--n-gen 0", "-pg 256,64", "-pg 1024,128"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("argv %q missing %q", command, expected)
		}
	}
	if _, err := BuildArgv("bench", "model.gguf", MappedConfig{}, workload, testCapabilities()); !errors.Is(err, ErrInvalidWorkload) {
		t.Fatalf("missing pg capability err=%v", err)
	}
}

func TestParseCombinedContextDepthCase(t *testing.T) {
	results, err := ParseMachineOutput([]byte(`[{"n_prompt":1024,"n_gen":128,"n_depth":2048,"avg_ts":38.5}]`), WorkloadProfile{Repetitions: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].CaseID != "pg-1024-128-d2048" || results[0].PromptTokens != 1024 || results[0].GenerationTokens != 128 || results[0].ContextDepth != 2048 {
		t.Fatalf("results=%+v", results)
	}
}

func TestDiscoverCapabilitiesFiltersOptionalWorkloadPresets(t *testing.T) {
	for _, tc := range []struct {
		name      string
		optional  string
		wantPG    bool
		wantDepth bool
	}{
		{name: "base"},
		{name: "pg", optional: "-pg <pp,tg>  combined\\n", wantPG: true},
		{name: "depth", optional: "-d, --n-depth <n>  depth\\n", wantDepth: true},
		{name: "both", optional: "-pg <pp,tg>  combined\\n-d, --n-depth <n>  depth\\n", wantPG: true, wantDepth: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caps, err := DiscoverCapabilities(context.Background(), writeBenchScript(t, tc.name, tc.optional))
			if err != nil || !caps.Available {
				t.Fatalf("caps=%+v err=%v", caps, err)
			}
			field := func(key string) bool {
				for _, candidate := range caps.Workload.Fields {
					if candidate.Key == key {
						return true
					}
				}
				return false
			}
			preset := func(id string) bool {
				for _, candidate := range caps.Workload.Presets {
					if candidate.ID == id {
						return true
					}
				}
				return false
			}
			if field("combined_cases") != tc.wantPG || preset("chat-turn-v1") != tc.wantPG {
				t.Fatalf("PG capability fields=%+v presets=%+v", caps.Workload.Fields, caps.Workload.Presets)
			}
			if field("context_depths") != tc.wantDepth || preset("context-depth-v1") != tc.wantDepth {
				t.Fatalf("depth capability fields=%+v presets=%+v", caps.Workload.Fields, caps.Workload.Presets)
			}
			if !preset(DefaultWorkloadID) {
				t.Fatal("default workload was filtered")
			}
			if tc.wantPG && !contains(caps.SupportedOptions, "pg") {
				t.Fatalf("supported options=%v", caps.SupportedOptions)
			}
		})
	}
}

func TestOptionalWorkloadPresetResolution(t *testing.T) {
	for _, tc := range []struct {
		id           string
		wantCombined bool
		wantDepth    bool
	}{
		{id: "chat-turn-v1", wantCombined: true},
		{id: "context-depth-v1", wantDepth: true},
	} {
		workload, err := NormalizeWorkload(&WorkloadProfile{ID: tc.id})
		if err != nil {
			t.Fatal(err)
		}
		if (len(workload.CombinedCases) > 0) != tc.wantCombined || (len(workload.ContextDepths) > 0) != tc.wantDepth {
			t.Fatalf("preset %s resolved=%+v", tc.id, workload)
		}
	}
}
