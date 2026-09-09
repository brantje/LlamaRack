package benchmark

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

func TestNormalizeWorkloadAndContext(t *testing.T) {
	defaults, err := NormalizeWorkload(nil)
	if err != nil || defaults.ID != DefaultWorkloadID || defaults.Repetitions != 5 || !defaults.Warmup {
		t.Fatalf("defaults=%+v err=%v", defaults, err)
	}
	custom, err := NormalizeWorkload(&WorkloadProfile{PromptTokens: []int{512, 512, 2048}, GenerationTokens: []int{128}, Repetitions: 3, Warmup: false})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(custom.PromptTokens, []int{512, 2048}) || custom.Version != WorkloadSchemaVersion || custom.ID != DefaultWorkloadID {
		t.Fatalf("normalized=%+v", custom)
	}
	if err := ValidateWorkloadContext(custom, InstanceConfigSnapshot{Options: map[string]string{"ctx-size": "1024"}}); !errors.Is(err, ErrInvalidWorkload) {
		t.Fatalf("context validation err=%v", err)
	}
	if _, err := NormalizeWorkload(&WorkloadProfile{PromptTokens: []int{1}, Repetitions: maxWorkloadRepetitions + 1}); !errors.Is(err, ErrInvalidWorkload) {
		t.Fatalf("repetition validation err=%v", err)
	}
}

func TestMapInstanceConfigAndBuildArgv(t *testing.T) {
	caps := testCapabilities(
		llamacpp.Option{Key: "batch-size", Kind: "integer"},
		llamacpp.Option{Key: "flash-attn", Kind: "boolean"},
		llamacpp.Option{Key: "no-flash-attn", Kind: "boolean"},
		llamacpp.Option{Key: "device", Kind: "string"},
		llamacpp.Option{Key: "tensor-split", Kind: "string"},
		llamacpp.Option{Key: "no-warmup", Kind: "boolean"},
	)
	config := InstanceConfigSnapshot{
		SchemaVersion: ConfigSchemaVersion,
		GPUMode:       "manual",
		GPUDevices:    []string{"CUDA0", "CUDA1"},
		TensorSplit:   "1,1",
		Options:       map[string]string{"ctx-size": "4096", "batch-size": "512", "flash-attn": "false", "port": "8000"},
		Sources:       map[string]string{"ctx-size": "instance", "batch-size": "instance", "flash-attn": "instance", "port": "global"},
	}
	mapped, err := MapInstanceConfig(config, scheduler.Placement{Devices: []string{"CUDA0", "CUDA1"}, TensorSplit: "2,1", Fits: true}, caps)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(mapped.Args, " ")
	for _, expected := range []string{"--batch-size 512", "--no-flash-attn", "--device CUDA0/CUDA1", "--tensor-split 1/1"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("mapped args %q missing %q", joined, expected)
		}
	}
	if len(mapped.Differences) != 2 || mapped.Differences[0].Key != "ctx-size" {
		t.Fatalf("differences=%+v", mapped.Differences)
	}
	prefixed, err := MapInstanceConfig(InstanceConfigSnapshot{
		Options: map[string]string{"--batch-size": "512", "--flash-attn": "false", "--port": "8000"},
		Sources: map[string]string{"--batch-size": "instance", "--flash-attn": "instance", "--port": "global"},
	}, scheduler.Placement{}, caps)
	if err != nil {
		t.Fatal(err)
	}
	prefixedArgs := strings.Join(prefixed.Args, " ")
	if !strings.Contains(prefixedArgs, "--batch-size 512") || !strings.Contains(prefixedArgs, "--no-flash-attn") {
		t.Fatalf("prefixed option keys dropped values: %q", prefixedArgs)
	}
	if len(prefixed.Differences) != 1 || prefixed.Differences[0].Key != "port" || prefixed.Differences[0].Severity != "ignored" {
		t.Fatalf("prefixed sources=%+v", prefixed.Differences)
	}
	workload := WorkloadProfile{ID: DefaultWorkloadID, Version: WorkloadSchemaVersion, PromptTokens: []int{512, 2048}, GenerationTokens: []int{128}, Repetitions: 3, Warmup: false}
	argv, err := BuildArgv("/app/llama-bench", "/models/model.gguf", mapped, workload, caps)
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(argv, " ")
	for _, expected := range []string{"/app/llama-bench --model /models/model.gguf", "--n-prompt 512,2048", "--n-gen 128", "--repetitions 3", "--output json", "--no-warmup"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("argv %q missing %q", command, expected)
		}
	}
}

func TestMapInstanceConfigRejectsMaterialUnsupportedSetting(t *testing.T) {
	caps := testCapabilities(llamacpp.Option{Key: "device", Kind: "string"})
	config := InstanceConfigSnapshot{Options: map[string]string{"spec-draft-model": "/models/draft.gguf"}, Sources: map[string]string{"spec-draft-model": "instance"}}
	mapped, err := MapInstanceConfig(config, scheduler.Placement{}, caps)
	if !errors.Is(err, ErrUnsupportedConfig) {
		t.Fatalf("mapped=%+v err=%v", mapped, err)
	}
	if len(mapped.Differences) != 1 || mapped.Differences[0].Severity != "blocking" {
		t.Fatalf("differences=%+v", mapped.Differences)
	}
}

func TestParseMachineOutputPreservesUnknownFields(t *testing.T) {
	workload := DefaultWorkload()
	data := []byte(`[
		{"n_prompt":512,"n_gen":0,"avg_ns":1000,"stddev_ns":50,"avg_ts":123.4,"stddev_ts":1.2,"samples_ts":[120,124,126],"future_field":{"x":1}},
		{"n_prompt":0,"n_gen":128,"avg_ts":45.6,"stddev_ts":0.4,"samples_ts":[45,46]}
	]`)
	results, err := ParseMachineOutput(data, workload)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].CaseID != "pp-512" || results[1].CaseID != "tg-128" || results[0].Repetitions != 3 {
		t.Fatalf("results=%+v", results)
	}
	if !strings.Contains(string(results[0].RawFields), "future_field") {
		t.Fatalf("unknown field lost: %s", results[0].RawFields)
	}

	jsonl := []byte("{\"n_prompt\":16,\"avg_ts\":9.5}\n{\"n_gen\":8,\"avg_ts\":4.25}\n")
	results, err = ParseMachineOutput(jsonl, WorkloadProfile{Repetitions: 7})
	if err != nil || len(results) != 2 || results[0].Repetitions != 7 {
		t.Fatalf("jsonl results=%+v err=%v", results, err)
	}
	if _, err := ParseMachineOutput([]byte("not-json"), workload); err == nil {
		t.Fatal("expected malformed output error")
	}
}

func TestDiscoverCapabilitiesRequiresMachineReadableContract(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "llama-bench")
	script := `#!/bin/sh
case "$1" in
  --version) echo "llama-bench test-build" ;;
  --help) cat <<'HELP'
--model <FNAME>  model
--output <csv|json|jsonl>  output
--repetitions <n>  repetitions
--n-prompt <n>  prompt
--n-gen <n>  generation
--no-warmup  no warmup
--device <devs>  devices
HELP
  ;;
  --list-devices) echo "Available devices:" ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	caps, err := DiscoverCapabilities(context.Background(), path)
	if err != nil || !caps.Available || caps.OutputFormat != "json" || !contains(caps.SupportedOptions, "n-gen") {
		t.Fatalf("caps=%+v err=%v", caps, err)
	}

	bad := filepath.Join(dir, "llama-bench-old")
	if err := os.WriteFile(bad, []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo old; else echo '--model <FNAME>  model'; fi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	caps, err = DiscoverCapabilities(context.Background(), bad)
	if err != nil || caps.Available || !strings.Contains(caps.Reason, "missing required options") {
		t.Fatalf("old caps=%+v err=%v", caps, err)
	}
}

func testCapabilities(extra ...llamacpp.Option) Capabilities {
	options := []llamacpp.Option{
		{Key: "model", Kind: "string"}, {Key: "output", Kind: "enum", Choices: []string{"json"}},
		{Key: "repetitions", Kind: "integer"}, {Key: "n-prompt", Kind: "integer"}, {Key: "n-gen", Kind: "integer"},
		{Key: "threads", Kind: "integer", Description: "(default: 4)"},
	}
	options = append(options, extra...)
	profile := llamacpp.Profile{Version: "test", Fingerprint: "fingerprint", Options: options}
	return Capabilities{Available: true, Version: "test", Fingerprint: "fingerprint", OutputFormat: "json", Workload: DefaultWorkloadSchema(), profile: profile}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestWorkloadArgvDisablesUnselectedCases(t *testing.T) {
	for _, tc := range []struct {
		name        string
		prompt, gen []int
		want        string
	}{
		{"prompt only", []int{512}, nil, "--n-prompt 512 --n-gen 0"},
		{"generation only", nil, []int{128}, "--n-prompt 0 --n-gen 128"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv, err := BuildArgv("bench", "model.gguf", MappedConfig{}, WorkloadProfile{PromptTokens: tc.prompt, GenerationTokens: tc.gen, Repetitions: 2, Warmup: true}, testCapabilities())
			if err != nil || !strings.Contains(strings.Join(argv, " "), tc.want) {
				t.Fatalf("argv=%v err=%v", argv, err)
			}
		})
	}
}

func TestMapBenchNumericBooleans(t *testing.T) {
	caps := testCapabilities(llamacpp.Option{Key: "no-kv-offload", Kind: "enum", Choices: []string{"0", "1"}})
	for _, value := range []string{"", "true", "1", "yes", "on", "false", "0", "no", "off", "invalid"} {
		t.Run(value, func(t *testing.T) {
			mapped, err := MapInstanceConfig(InstanceConfigSnapshot{Options: map[string]string{"no-kv-offload": value}}, scheduler.Placement{}, caps)
			if value == "invalid" {
				if !errors.Is(err, ErrUnsupportedConfig) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			want := "1"
			if value == "false" || value == "0" || value == "no" || value == "off" {
				want = "0"
			}
			if err != nil || !reflect.DeepEqual(mapped.Args, []string{"--no-kv-offload", want}) {
				t.Fatalf("mapped=%+v err=%v", mapped, err)
			}
		})
	}
}

func TestAdapterRejectsUnavailableAndUnrepresentableConfigurations(t *testing.T) {
	if _, err := MapInstanceConfig(InstanceConfigSnapshot{}, scheduler.Placement{}, Capabilities{Reason: "missing"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if _, err := BuildArgv("bench", "model", MappedConfig{}, DefaultWorkload(), Capabilities{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
	for _, config := range []InstanceConfigSnapshot{
		{Options: map[string]string{"": "ignored", "detected": "x"}, Sources: map[string]string{"detected": "detected"}},
		{TensorSplit: "1,1"},
	} {
		mapped, err := MapInstanceConfig(config, scheduler.Placement{}, testCapabilities())
		if config.TensorSplit != "" {
			if !errors.Is(err, ErrUnsupportedConfig) {
				t.Fatalf("err=%v", err)
			}
		} else if err != nil || len(mapped.Differences) != 1 || mapped.Differences[0].Severity != "ignored" {
			t.Fatalf("mapped=%+v err=%v", mapped, err)
		}
	}
	if _, err := MapInstanceConfig(InstanceConfigSnapshot{}, scheduler.Placement{Devices: []string{"CUDA0"}}, testCapabilities()); !errors.Is(err, ErrUnsupportedConfig) {
		t.Fatalf("device error=%v", err)
	}
	caps := testCapabilities(llamacpp.Option{Key: "no-mmap", Kind: "boolean"}, llamacpp.Option{Key: "mmap", Kind: "boolean"})
	mapped, err := MapInstanceConfig(InstanceConfigSnapshot{Options: map[string]string{"no-mmap": "false"}}, scheduler.Placement{}, caps)
	if err != nil || !reflect.DeepEqual(mapped.Args, []string{"--mmap"}) {
		t.Fatalf("mapped=%+v err=%v", mapped, err)
	}
	for _, value := range []string{"false", "invalid"} {
		caps := testCapabilities(llamacpp.Option{Key: "mlock", Kind: "boolean"})
		if _, err := MapInstanceConfig(InstanceConfigSnapshot{Options: map[string]string{"mlock": value}}, scheduler.Placement{}, caps); !errors.Is(err, ErrUnsupportedConfig) {
			t.Fatalf("value=%s err=%v", value, err)
		}
	}
	for _, path := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		caps, err := DiscoverCapabilities(context.Background(), path)
		if err != nil || caps.Available || caps.Reason == "" {
			t.Fatalf("caps=%+v err=%v", caps, err)
		}
	}
}

func TestWorkloadValidationRejectsInvalidCases(t *testing.T) {
	for _, input := range []WorkloadProfile{
		{Version: 99, Repetitions: 1, PromptTokens: []int{1}},
		{Repetitions: 1, PromptTokens: []int{-1}},
		{Repetitions: 1, GenerationTokens: []int{maxWorkloadTokens + 1}},
		{Repetitions: 1},
	} {
		if _, err := NormalizeWorkload(&input); !errors.Is(err, ErrInvalidWorkload) {
			t.Fatalf("input=%+v err=%v", input, err)
		}
	}
	if err := ValidateWorkloadContext(DefaultWorkload(), InstanceConfigSnapshot{Options: map[string]string{"ctx-size": "bad"}}); !errors.Is(err, ErrUnsupportedConfig) {
		t.Fatalf("err=%v", err)
	}
	if err := ValidateWorkloadContext(DefaultWorkload(), InstanceConfigSnapshot{Options: map[string]string{"ctx-size": "4096"}}); err != nil {
		t.Fatal(err)
	}
}
