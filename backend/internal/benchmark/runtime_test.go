package benchmark

import (
	"reflect"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

func runtimeTestCapabilities(options ...llamacpp.Option) Capabilities {
	profile := llamacpp.Profile{
		Options: append([]llamacpp.Option{
			{Key: "model", Kind: "string"}, {Key: "output", Kind: "enum", Choices: []string{"json"}},
			{Key: "repetitions", Kind: "integer"}, {Key: "n-prompt", Kind: "integer"}, {Key: "n-gen", Kind: "integer"},
			{Key: "threads", Kind: "integer", Description: "threads (default: 4)"},
		}, options...),
		Devices: []string{"CUDA0", "CUDA1"}, DeviceDiscoveryAvailable: true,
	}
	return Capabilities{Available: true, RuntimeOptions: runtimeFieldsForProfile(profile), profile: profile}
}

func intp(value int) *int             { return &value }
func boolp(value bool) *bool          { return &value }
func strp(value string) *string       { return &value }
func slicep(value []string) *[]string { return &value }

func TestResolveRuntimeConfigComposesAndNormalizesOverrides(t *testing.T) {
	caps := runtimeTestCapabilities(
		llamacpp.Option{Key: "ctx-size", Kind: "integer"},
		llamacpp.Option{Key: "batch-size", Kind: "integer"},
		llamacpp.Option{Key: "threads", Kind: "integer"},
		llamacpp.Option{Key: "flash-attn", Kind: "boolean"},
		llamacpp.Option{Key: "no-flash-attn", Kind: "boolean"},
		llamacpp.Option{Key: "device", Kind: "string"},
		llamacpp.Option{Key: "tensor-split", Kind: "string"},
	)
	instance := InstanceConfigSnapshot{SchemaVersion: 1, GPUMode: "auto", Options: map[string]string{"ctx-size": "4096", "batch-size": "512", "threads": "4", "flash-attn": "false"}, Sources: map[string]string{"ctx-size": "instance", "batch-size": "instance", "threads": "instance", "flash-attn": "instance"}}
	overrides, effective, err := ResolveRuntimeConfig(instance, RuntimeOverrides{ContextSize: intp(8192), BatchSize: intp(1024), Threads: intp(4), FlashAttention: boolp(true), GPUDevices: slicep([]string{"CUDA1", "CUDA0"}), TensorSplit: strp("2,1")}, caps)
	if err != nil {
		t.Fatal(err)
	}
	if overrides.Threads != nil {
		t.Fatalf("equal-to-Instance value persisted as override: %+v", overrides)
	}
	if effective.Options["ctx-size"] != "8192" || effective.Options["batch-size"] != "1024" || effective.Options["threads"] != "4" || effective.Options["flash-attn"] != "true" {
		t.Fatalf("effective=%+v", effective)
	}
	if effective.GPUMode != "manual" || !reflect.DeepEqual(effective.GPUDevices, []string{"CUDA1", "CUDA0"}) || effective.TensorSplit != "2,1" {
		t.Fatalf("placement=%+v", effective)
	}
	if instance.Options["ctx-size"] != "4096" || instance.GPUMode != "auto" {
		t.Fatalf("Instance snapshot mutated: %+v", instance)
	}
}

func TestResolveRuntimeConfigRejectsUnsupportedMalformedAndInjectionInputs(t *testing.T) {
	base := InstanceConfigSnapshot{SchemaVersion: 1, GPUMode: "auto", Options: map[string]string{}, Sources: map[string]string{}}
	if _, _, err := ResolveRuntimeConfig(base, RuntimeOverrides{BatchSize: intp(1024)}, runtimeTestCapabilities()); err == nil || !strings.Contains(err.Error(), "batch_size") {
		t.Fatalf("unsupported override err=%v", err)
	}
	caps := runtimeTestCapabilities(
		llamacpp.Option{Key: "batch-size", Kind: "integer"},
		llamacpp.Option{Key: "cache-type-k", Kind: "enum", Choices: []string{"f16", "q8_0"}},
		llamacpp.Option{Key: "device", Kind: "string"},
		llamacpp.Option{Key: "tensor-split", Kind: "string"},
	)
	for _, tc := range []RuntimeOverrides{
		{BatchSize: intp(0)},
		{CacheTypeK: strp("q8_0 --model /tmp/evil.gguf")},
		{GPUDevices: slicep([]string{"CUDA0/--model"})},
		{GPUDevices: slicep([]string{"CUDA9"})},
		{GPUDevices: slicep([]string{"CUDA0", "CUDA1"}), TensorSplit: strp("1;--help")},
	} {
		if _, _, err := ResolveRuntimeConfig(base, tc, caps); err == nil {
			t.Fatalf("accepted malicious/malformed override: %+v", tc)
		}
	}
}

func TestRuntimeCapabilitiesExposeOnlySafeDetectedFields(t *testing.T) {
	profile := llamacpp.Profile{Options: []llamacpp.Option{
		{Key: "batch-size", Kind: "integer", Description: "batch (default: 512)"},
		{Key: "spec-draft-model", Kind: "string"}, {Key: "mmproj", Kind: "string"}, {Key: "model", Kind: "string"},
		{Key: "device", Kind: "string"},
	}, Devices: []string{"CUDA0"}, DeviceDiscoveryAvailable: true}
	fields := runtimeFieldsForProfile(profile)
	keys := map[string]RuntimeOptionField{}
	for _, field := range fields {
		keys[field.Key] = field
	}
	if keys["batch_size"].DefaultValue != "512" || !keys["batch_size"].InstanceEditable {
		t.Fatalf("batch field=%+v", keys["batch_size"])
	}
	if !reflect.DeepEqual(keys["gpu_devices"].Choices, []string{"CUDA0"}) {
		t.Fatalf("device field=%+v", keys["gpu_devices"])
	}
	for _, forbidden := range []string{"spec-draft-model", "mmproj", "model", "executable", "argv", "path"} {
		if _, ok := keys[forbidden]; ok {
			t.Fatalf("unsafe capability exposed: %s", forbidden)
		}
	}
}

func TestMapInstanceConfigExecutesContextWhenLlamaBenchSupportsIt(t *testing.T) {
	caps := runtimeTestCapabilities(llamacpp.Option{Key: "ctx-size", Kind: "integer"})
	mapped, err := MapInstanceConfig(InstanceConfigSnapshot{Options: map[string]string{"ctx-size": "8192"}, Sources: map[string]string{"ctx-size": "benchmark"}}, scheduler.Placement{}, caps)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(mapped.Args, " "); !strings.Contains(got, "--ctx-size 8192") {
		t.Fatalf("args=%q", got)
	}
}
