package benchmark

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
)

func TestRuntimeHelpersCoverEveryOverrideKind(t *testing.T) {
	if got := normalizedDevices([]string{" CUDA0 ", "", "CUDA0", "CUDA1"}); !reflect.DeepEqual(got, []string{"CUDA0", "CUDA1"}) {
		t.Fatalf("normalized devices=%v", got)
	}
	if !containsFold([]string{" row ", "layer"}, "ROW") || containsFold([]string{"row"}, "none") {
		t.Fatal("containsFold did not compare choices case-insensitively")
	}
	for raw, want := range map[string]bool{"true": true, "1": true, "yes": true, "on": true, "false": false, "0": false, "no": false, "off": false} {
		if !sameBoolValue(raw, want) {
			t.Fatalf("sameBoolValue(%q,%v)=false", raw, want)
		}
	}
	if sameBoolValue("", true) || sameBoolValue("maybe", true) || sameBoolValue("true", false) {
		t.Fatal("sameBoolValue accepted a non-equivalent value")
	}

	var overrides RuntimeOverrides
	for key, value := range map[string]int{
		"context_size": 8192,
		"gpu_layers":   -1,
		"batch_size":   1024,
		"ubatch_size":  256,
		"threads":      8,
		"main_gpu":     1,
		"n_cpu_moe":    4,
		"ignored":      99,
	} {
		setRuntimeInt(&overrides, key, value)
	}
	for key, value := range map[string]bool{
		"flash_attention": true,
		"kv_offload":      false,
		"mmap":            false,
		"mlock":           true,
		"ignored":         true,
	} {
		setRuntimeBool(&overrides, key, value)
	}
	for key, value := range map[string]string{
		"cache_type_k": "q8_0",
		"cache_type_v": "f16",
		"split_mode":   "row",
		"ignored":      "value",
	} {
		setRuntimeString(&overrides, key, value)
	}
	devices := []string{"CUDA0", "CUDA1"}
	split := "2,1"
	overrides.GPUDevices = &devices
	overrides.TensorSplit = &split

	wantKeys := []string{"batch_size", "cache_type_k", "cache_type_v", "context_size", "flash_attention", "gpu_devices", "gpu_layers", "kv_offload", "main_gpu", "mlock", "mmap", "n_cpu_moe", "split_mode", "tensor_split", "threads", "ubatch_size"}
	if got := RuntimeOverrideKeys(overrides); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("override keys=%v want=%v", got, wantKeys)
	}
	if choices := runtimeFieldChoices(Capabilities{}, "missing"); choices != nil {
		t.Fatalf("missing choices=%v", choices)
	}
}

func TestResolveRuntimeConfigCoversAllSupportedOverrideTypes(t *testing.T) {
	caps := runtimeTestCapabilities(
		llamacpp.Option{Key: "ctx-size", Kind: "integer"},
		llamacpp.Option{Key: "n-gpu-layers", Kind: "integer"},
		llamacpp.Option{Key: "batch-size", Kind: "integer"},
		llamacpp.Option{Key: "ubatch-size", Kind: "integer"},
		llamacpp.Option{Key: "threads", Kind: "integer"},
		llamacpp.Option{Key: "cache-type-k", Kind: "enum", Choices: []string{"f16", "q8_0"}},
		llamacpp.Option{Key: "cache-type-v", Kind: "enum", Choices: []string{"f16", "q8_0"}},
		llamacpp.Option{Key: "flash-attn", Kind: "boolean"},
		llamacpp.Option{Key: "no-flash-attn", Kind: "boolean"},
		llamacpp.Option{Key: "no-kv-offload", Kind: "enum", Choices: []string{"0", "1"}},
		llamacpp.Option{Key: "split-mode", Kind: "enum", Choices: []string{"none", "layer", "row"}},
		llamacpp.Option{Key: "main-gpu", Kind: "integer"},
		llamacpp.Option{Key: "n-cpu-moe", Kind: "integer"},
		llamacpp.Option{Key: "mmap", Kind: "boolean"},
		llamacpp.Option{Key: "no-mmap", Kind: "boolean"},
		llamacpp.Option{Key: "mlock", Kind: "boolean"},
		llamacpp.Option{Key: "no-mlock", Kind: "boolean"},
		llamacpp.Option{Key: "device", Kind: "string"},
		llamacpp.Option{Key: "tensor-split", Kind: "string"},
	)
	instance := InstanceConfigSnapshot{
		SchemaVersion: 1,
		GPUMode:       "manual",
		GPUDevices:    []string{"CUDA0"},
		TensorSplit:   "1",
		Options: map[string]string{
			"ctx-size": "4096", "n-gpu-layers": "0", "batch-size": "512", "ubatch-size": "128", "threads": "4",
			"cache-type-k": "f16", "cache-type-v": "f16", "flash-attn": "false", "kv-offload": "true", "split-mode": "layer",
			"main-gpu": "0", "n-cpu-moe": "0", "mmap": "true", "mlock": "false",
		},
		Sources: map[string]string{},
	}
	requested := RuntimeOverrides{
		ContextSize: intp(8192), GPULayers: intp(-1), BatchSize: intp(1024), UBatchSize: intp(256), Threads: intp(8),
		CacheTypeK: strp("Q8_0"), CacheTypeV: strp("q8_0"), FlashAttention: boolp(true), KVOffload: boolp(false),
		SplitMode: strp("ROW"), MainGPU: intp(1), NCPUMoE: intp(2), MMap: boolp(false), MLock: boolp(true),
		GPUDevices: slicep([]string{" CUDA0 ", "CUDA1", "CUDA1"}), TensorSplit: strp(" 2, 1 "),
	}

	normalized, effective, err := ResolveRuntimeConfig(instance, requested, caps)
	if err != nil {
		t.Fatal(err)
	}
	if got := RuntimeOverrideKeys(normalized); len(got) != len(runtimeOverrideSpecs) {
		t.Fatalf("normalized keys=%v count=%d want=%d", got, len(got), len(runtimeOverrideSpecs))
	}
	if effective.GPUMode != "manual" || !reflect.DeepEqual(effective.GPUDevices, []string{"CUDA0", "CUDA1"}) || effective.TensorSplit != "2,1" {
		t.Fatalf("effective placement=%+v", effective)
	}
	for _, key := range []string{"ctx-size", "n-gpu-layers", "batch-size", "ubatch-size", "threads", "cache-type-k", "cache-type-v", "flash-attn", "kv-offload", "split-mode", "main-gpu", "n-cpu-moe", "mmap", "mlock"} {
		if effective.Sources[key] != "benchmark" {
			t.Fatalf("source[%s]=%q", key, effective.Sources[key])
		}
	}
	if effective.Options["cache-type-k"] != "Q8_0" || effective.Options["split-mode"] != "ROW" {
		t.Fatalf("string overrides not preserved after validation: %+v", effective.Options)
	}
	if instance.Options["ctx-size"] != "4096" || len(instance.GPUDevices) != 1 {
		t.Fatalf("input snapshot mutated: %+v", instance)
	}
}

func TestResolveRuntimeConfigRemainingErrorAndPlacementBranches(t *testing.T) {
	base := InstanceConfigSnapshot{SchemaVersion: 1, GPUMode: "auto", Options: map[string]string{}, Sources: map[string]string{}}
	if _, _, err := ResolveRuntimeConfig(base, RuntimeOverrides{}, Capabilities{Reason: "disabled"}); err == nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable err=%v", err)
	}

	stringCaps := runtimeTestCapabilities(
		llamacpp.Option{Key: "cache-type-k", Kind: "enum", Choices: []string{"f16", "q8_0"}},
		llamacpp.Option{Key: "split-mode", Kind: "enum"},
		llamacpp.Option{Key: "device", Kind: "string"},
		llamacpp.Option{Key: "tensor-split", Kind: "string"},
	)
	for _, tc := range []struct {
		name string
		base InstanceConfigSnapshot
		over RuntimeOverrides
		want string
	}{
		{"empty enum", base, RuntimeOverrides{CacheTypeK: strp("")}, "invalid value"},
		{"enum not advertised", base, RuntimeOverrides{CacheTypeK: strp("q4_0")}, "not one of"},
		{"split fallback invalid", base, RuntimeOverrides{SplitMode: strp("column")}, "not one of"},
		{"manual empty devices", InstanceConfigSnapshot{SchemaVersion: 1, GPUMode: "manual", Options: map[string]string{}, Sources: map[string]string{}}, RuntimeOverrides{}, "manual GPU placement"},
		{"auto split", InstanceConfigSnapshot{SchemaVersion: 1, GPUMode: "auto", TensorSplit: "1", Options: map[string]string{}, Sources: map[string]string{}}, RuntimeOverrides{}, "requires explicit GPU devices"},
		{"split count", InstanceConfigSnapshot{SchemaVersion: 1, GPUMode: "manual", GPUDevices: []string{"CUDA0", "CUDA1"}, TensorSplit: "1", Options: map[string]string{}, Sources: map[string]string{}}, RuntimeOverrides{}, "one weight per selected GPU"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ResolveRuntimeConfig(tc.base, tc.over, stringCaps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want substring %q", err, tc.want)
			}
		})
	}

	autoWithStaleSplit := InstanceConfigSnapshot{SchemaVersion: 1, GPUMode: "manual", GPUDevices: []string{"CUDA0"}, TensorSplit: "1", Options: map[string]string{}, Sources: map[string]string{}}
	normalized, effective, err := ResolveRuntimeConfig(autoWithStaleSplit, RuntimeOverrides{GPUDevices: slicep([]string{})}, stringCaps)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.GPUDevices == nil || effective.GPUMode != "auto" || effective.TensorSplit != "" {
		t.Fatalf("automatic placement did not clear split: normalized=%+v effective=%+v", normalized, effective)
	}
}

func TestRuntimeBooleanRepresentabilityBranches(t *testing.T) {
	binary := llamacpp.Profile{Options: []llamacpp.Option{{Key: "mmap", Kind: "enum", Choices: []string{"0", "1"}}}}
	if !runtimeBooleanRepresentable(binary, "mmap") {
		t.Fatal("binary choice should represent a boolean")
	}
	paired := llamacpp.Profile{Options: []llamacpp.Option{{Key: "flash-attn", Kind: "boolean"}, {Key: "no-flash-attn", Kind: "boolean"}}}
	if !runtimeBooleanRepresentable(paired, "flash-attn") {
		t.Fatal("paired boolean flags should be representable")
	}
	if runtimeBooleanRepresentable(llamacpp.Profile{}, "mmap") {
		t.Fatal("missing non-KV boolean should not be representable")
	}
	kvFallback := llamacpp.Profile{Options: []llamacpp.Option{{Key: "no-kv-offload", Kind: "enum", Choices: []string{"0", "1"}}}}
	if !runtimeBooleanRepresentable(kvFallback, "kv-offload") {
		t.Fatal("binary no-kv-offload should represent kv-offload")
	}
	notBoolean := llamacpp.Profile{Options: []llamacpp.Option{{Key: "mmap", Kind: "string"}}}
	if runtimeBooleanRepresentable(notBoolean, "mmap") {
		t.Fatal("string option should not represent a boolean")
	}
}
