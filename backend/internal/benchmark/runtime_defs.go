package benchmark

import (
	"regexp"
	"strings"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
)

type RuntimeOverrides struct {
	ContextSize    *int      `json:"context_size,omitempty"`
	GPUDevices     *[]string `json:"gpu_devices,omitempty"`
	TensorSplit    *string   `json:"tensor_split,omitempty"`
	GPULayers      *int      `json:"gpu_layers,omitempty"`
	BatchSize      *int      `json:"batch_size,omitempty"`
	UBatchSize     *int      `json:"ubatch_size,omitempty"`
	Threads        *int      `json:"threads,omitempty"`
	CacheTypeK     *string   `json:"cache_type_k,omitempty"`
	CacheTypeV     *string   `json:"cache_type_v,omitempty"`
	FlashAttention *bool     `json:"flash_attention,omitempty"`
	KVOffload      *bool     `json:"kv_offload,omitempty"`
	SplitMode      *string   `json:"split_mode,omitempty"`
	MainGPU        *int      `json:"main_gpu,omitempty"`
	NCPUMoE        *int      `json:"n_cpu_moe,omitempty"`
	MMap           *bool     `json:"mmap,omitempty"`
	MLock          *bool     `json:"mlock,omitempty"`
}

type RuntimeOptionField struct {
	Key              string   `json:"key"`
	Label            string   `json:"label"`
	Kind             string   `json:"kind"`
	Option           string   `json:"option,omitempty"`
	InstanceOption   string   `json:"instance_option,omitempty"`
	InstanceField    string   `json:"instance_field,omitempty"`
	InstanceEditable bool     `json:"instance_editable"`
	BenchmarkOnly    bool     `json:"benchmark_only,omitempty"`
	Choices          []string `json:"choices,omitempty"`
	DefaultValue     string   `json:"default_value,omitempty"`
	Minimum          *int     `json:"minimum,omitempty"`
	Maximum          *int     `json:"maximum,omitempty"`
	Description      string   `json:"description,omitempty"`
}

type runtimeOverrideSpec struct {
	key              string
	label            string
	kind             string
	option           string
	instanceOption   string
	instanceField    string
	instanceEditable bool
	minimum          int
	maximum          int
}

var runtimeOverrideSpecs = []runtimeOverrideSpec{
	{key: "context_size", label: "Context size", kind: "integer", option: "ctx-size", instanceOption: "ctx-size", instanceEditable: true, minimum: 1, maximum: 4194304},
	{key: "gpu_devices", label: "GPU devices", kind: "device-list", option: "device", instanceField: "gpu_devices", instanceEditable: true},
	{key: "tensor_split", label: "Tensor split", kind: "tensor-split", option: "tensor-split", instanceField: "tensor_split", instanceEditable: true},
	{key: "gpu_layers", label: "GPU layers", kind: "integer", option: "n-gpu-layers", instanceOption: "n-gpu-layers", instanceEditable: true, minimum: -1, maximum: 100000},
	{key: "batch_size", label: "Batch size", kind: "integer", option: "batch-size", instanceOption: "batch-size", instanceEditable: true, minimum: 1, maximum: 4194304},
	{key: "ubatch_size", label: "Micro-batch size", kind: "integer", option: "ubatch-size", instanceOption: "ubatch-size", instanceEditable: true, minimum: 1, maximum: 4194304},
	{key: "threads", label: "Threads", kind: "integer", option: "threads", instanceOption: "threads", instanceEditable: true, minimum: 1, maximum: 65536},
	{key: "cache_type_k", label: "KV cache K type", kind: "enum", option: "cache-type-k", instanceOption: "cache-type-k", instanceEditable: true},
	{key: "cache_type_v", label: "KV cache V type", kind: "enum", option: "cache-type-v", instanceOption: "cache-type-v", instanceEditable: true},
	{key: "flash_attention", label: "Flash attention", kind: "boolean", option: "flash-attn", instanceOption: "flash-attn", instanceEditable: true},
	{key: "kv_offload", label: "KV cache offload", kind: "boolean", option: "kv-offload", instanceOption: "kv-offload", instanceEditable: true},
	{key: "split_mode", label: "Split mode", kind: "enum", option: "split-mode", instanceOption: "split-mode", instanceEditable: true},
	{key: "main_gpu", label: "Main GPU", kind: "integer", option: "main-gpu", instanceOption: "main-gpu", instanceEditable: true, minimum: 0, maximum: 65535},
	{key: "n_cpu_moe", label: "CPU MoE layers", kind: "integer", option: "n-cpu-moe", instanceOption: "n-cpu-moe", instanceEditable: true, minimum: 0, maximum: 100000},
	{key: "mmap", label: "Memory map model", kind: "boolean", option: "mmap", instanceOption: "mmap", instanceEditable: true},
	{key: "mlock", label: "Lock model in RAM", kind: "boolean", option: "mlock", instanceOption: "mlock", instanceEditable: true},
}

var simpleRuntimeValue = regexp.MustCompile(`^[A-Za-z0-9_.:+-]{1,64}$`)
var optionDefaultRE = regexp.MustCompile(`(?i)\(default:\s*([^\)]+)\)`)

func runtimeFieldsForProfile(profile llamacpp.Profile) []RuntimeOptionField {
	fields := make([]RuntimeOptionField, 0, len(runtimeOverrideSpecs))
	for _, spec := range runtimeOverrideSpecs {
		optionKey := spec.option
		option, supported := profileOption(profile, optionKey)
		if optionKey == "kv-offload" && !supported {
			option, supported = profileOption(profile, "no-kv-offload")
		}
		if !supported {
			continue
		}
		if spec.key == "gpu_devices" && !profile.DeviceDiscoveryAvailable {
			continue
		}
		if spec.kind == "boolean" && !runtimeBooleanRepresentable(profile, spec.option) {
			continue
		}
		if spec.kind == "enum" && len(option.Choices) == 0 && spec.key != "split_mode" {
			continue
		}
		field := RuntimeOptionField{
			Key: spec.key, Label: spec.label, Kind: spec.kind, Option: spec.option,
			InstanceOption: spec.instanceOption, InstanceField: spec.instanceField,
			InstanceEditable: spec.instanceEditable, BenchmarkOnly: !spec.instanceEditable,
			Description: option.Description,
		}
		if spec.kind == "integer" {
			minimum, maximum := spec.minimum, spec.maximum
			field.Minimum, field.Maximum = &minimum, &maximum
		}
		if spec.key == "gpu_devices" {
			field.Choices = append([]string(nil), profile.Devices...)
		} else if len(option.Choices) > 0 {
			field.Choices = append([]string(nil), option.Choices...)
		} else if spec.key == "split_mode" {
			field.Choices = []string{"none", "layer", "row"}
		}
		if match := optionDefaultRE.FindStringSubmatch(option.Description); len(match) == 2 {
			field.DefaultValue = strings.TrimSpace(match[1])
		}
		fields = append(fields, field)
	}
	return fields
}

func runtimeBooleanRepresentable(profile llamacpp.Profile, key string) bool {
	option, ok := profileOption(profile, key)
	if !ok {
		if key != "kv-offload" {
			return false
		}
		option, ok = profileOption(profile, "no-kv-offload")
		return ok && isBinaryChoiceOption(option)
	}
	if isBinaryChoiceOption(option) {
		return true
	}
	if !isBooleanBenchmarkOption(option) {
		return false
	}
	inverse, ok := profileOption(profile, inverseBenchmarkBoolean(key))
	return ok && isBooleanBenchmarkOption(inverse)
}

func hasRuntimeField(caps Capabilities, key string) bool {
	for _, field := range caps.RuntimeOptions {
		if field.Key == key {
			return true
		}
	}
	return false
}
