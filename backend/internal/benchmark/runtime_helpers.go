package benchmark

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func cloneConfigSnapshot(input InstanceConfigSnapshot) InstanceConfigSnapshot {
	return InstanceConfigSnapshot{
		SchemaVersion: input.SchemaVersion,
		GPUMode:       input.GPUMode,
		GPUDevices:    append([]string(nil), input.GPUDevices...),
		TensorSplit:   input.TensorSplit,
		Options:       cloneStringMap(input.Options),
		Sources:       cloneStringMap(input.Sources),
	}
}

func normalizedDevices(input []string) []string {
	out := make([]string, 0, len(input))
	seen := map[string]bool{}
	for _, raw := range input {
		value := strings.TrimSpace(raw)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func normalizeDevices(input, available []string) ([]string, error) {
	devices := normalizedDevices(input)
	allowed := map[string]bool{}
	for _, device := range available {
		allowed[device] = true
	}
	for _, device := range devices {
		if strings.ContainsAny(device, "/,;\x00\r\n\t ") {
			return nil, invalidOverride("gpu_devices", "contains an invalid device identifier")
		}
		if len(allowed) == 0 || !allowed[device] {
			return nil, invalidOverride("gpu_devices", "contains a device not advertised by llama-bench")
		}
	}
	return devices, nil
}

func normalizeTensorSplit(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		value, err := strconv.ParseFloat(part, 64)
		if err != nil || value <= 0 {
			return "", invalidOverride("tensor_split", "must be a comma-separated list of positive numbers")
		}
		out = append(out, part)
	}
	return strings.Join(out, ","), nil
}

func runtimeFieldChoices(caps Capabilities, key string) []string {
	for _, field := range caps.RuntimeOptions {
		if field.Key == key {
			return append([]string(nil), field.Choices...)
		}
	}
	return nil
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func sameOptionValue(left, right string) bool {
	return strings.TrimSpace(left) == strings.TrimSpace(right)
}
func sameBoolValue(raw string, want bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "true", "1", "yes", "on":
		return want && strings.TrimSpace(raw) != ""
	case "false", "0", "no", "off":
		return !want
	default:
		return false
	}
}

func unsupportedOverride(key string) error {
	return fmt.Errorf("%w: runtime override %q is not supported by this llama-bench build", ErrUnsupportedConfig, key)
}
func invalidOverride(key, reason string) error {
	return fmt.Errorf("%w: runtime override %q %s", ErrUnsupportedConfig, key, reason)
}

func stringPtr(value string) *string { return &value }

func setRuntimeInt(target *RuntimeOverrides, key string, value int) {
	switch key {
	case "context_size":
		target.ContextSize = &value
	case "gpu_layers":
		target.GPULayers = &value
	case "batch_size":
		target.BatchSize = &value
	case "ubatch_size":
		target.UBatchSize = &value
	case "threads":
		target.Threads = &value
	case "main_gpu":
		target.MainGPU = &value
	case "n_cpu_moe":
		target.NCPUMoE = &value
	}
}
func setRuntimeBool(target *RuntimeOverrides, key string, value bool) {
	switch key {
	case "flash_attention":
		target.FlashAttention = &value
	case "kv_offload":
		target.KVOffload = &value
	case "mmap":
		target.MMap = &value
	case "mlock":
		target.MLock = &value
	}
}
func setRuntimeString(target *RuntimeOverrides, key, value string) {
	switch key {
	case "cache_type_k":
		target.CacheTypeK = &value
	case "cache_type_v":
		target.CacheTypeV = &value
	case "split_mode":
		target.SplitMode = &value
	}
}

func RuntimeOverrideKeys(overrides RuntimeOverrides) []string {
	keys := make([]string, 0, len(runtimeOverrideSpecs))
	value := reflect.ValueOf(overrides)
	typeOf := value.Type()
	for i := 0; i < value.NumField(); i++ {
		if value.Field(i).IsNil() {
			continue
		}
		tag := strings.Split(typeOf.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" {
			keys = append(keys, tag)
		}
	}
	sort.Strings(keys)
	return keys
}
