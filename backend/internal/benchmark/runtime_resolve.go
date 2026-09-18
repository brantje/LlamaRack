package benchmark

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

func ResolveRuntimeConfig(instance InstanceConfigSnapshot, requested RuntimeOverrides, caps Capabilities) (RuntimeOverrides, InstanceConfigSnapshot, error) {
	if !caps.Available {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, fmt.Errorf("%w: %s", ErrUnavailable, caps.Reason)
	}
	effective := cloneConfigSnapshot(instance)
	normalized := RuntimeOverrides{}

	applyOptionInt := func(key, option string, input *int, minimum, maximum int) error {
		if input == nil {
			return nil
		}
		if !hasRuntimeField(caps, key) {
			return unsupportedOverride(key)
		}
		if *input < minimum || *input > maximum {
			return invalidOverride(key, fmt.Sprintf("must be between %d and %d", minimum, maximum))
		}
		value := strconv.Itoa(*input)
		if sameOptionValue(effective.Options[option], value) {
			return nil
		}
		effective.Options[option] = value
		effective.Sources[option] = "benchmark"
		setRuntimeInt(&normalized, key, *input)
		return nil
	}
	applyOptionBool := func(key, option string, input *bool) error {
		if input == nil {
			return nil
		}
		if !hasRuntimeField(caps, key) {
			return unsupportedOverride(key)
		}
		value := strconv.FormatBool(*input)
		if sameBoolValue(effective.Options[option], *input) {
			return nil
		}
		effective.Options[option] = value
		effective.Sources[option] = "benchmark"
		setRuntimeBool(&normalized, key, *input)
		return nil
	}
	applyOptionString := func(key, option string, input *string, fallbackChoices []string) error {
		if input == nil {
			return nil
		}
		if !hasRuntimeField(caps, key) {
			return unsupportedOverride(key)
		}
		value := strings.TrimSpace(*input)
		if value == "" || !simpleRuntimeValue.MatchString(value) {
			return invalidOverride(key, "contains an invalid value")
		}
		choices := runtimeFieldChoices(caps, key)
		if len(choices) == 0 {
			choices = fallbackChoices
		}
		if len(choices) > 0 && !containsFold(choices, value) {
			return invalidOverride(key, "is not one of the values advertised by llama-bench")
		}
		if sameOptionValue(effective.Options[option], value) {
			return nil
		}
		effective.Options[option] = value
		effective.Sources[option] = "benchmark"
		setRuntimeString(&normalized, key, value)
		return nil
	}

	if err := applyOptionInt("context_size", "ctx-size", requested.ContextSize, 1, 4194304); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionInt("gpu_layers", "n-gpu-layers", requested.GPULayers, -1, 100000); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionInt("batch_size", "batch-size", requested.BatchSize, 1, 4194304); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionInt("ubatch_size", "ubatch-size", requested.UBatchSize, 1, 4194304); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionInt("threads", "threads", requested.Threads, 1, 65536); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionString("cache_type_k", "cache-type-k", requested.CacheTypeK, nil); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionString("cache_type_v", "cache-type-v", requested.CacheTypeV, nil); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionBool("flash_attention", "flash-attn", requested.FlashAttention); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionBool("kv_offload", "kv-offload", requested.KVOffload); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionString("split_mode", "split-mode", requested.SplitMode, []string{"none", "layer", "row"}); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionInt("main_gpu", "main-gpu", requested.MainGPU, 0, 65535); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionInt("n_cpu_moe", "n-cpu-moe", requested.NCPUMoE, 0, 100000); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionBool("mmap", "mmap", requested.MMap); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}
	if err := applyOptionBool("mlock", "mlock", requested.MLock); err != nil {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
	}

	if requested.GPUDevices != nil {
		if !hasRuntimeField(caps, "gpu_devices") {
			return RuntimeOverrides{}, InstanceConfigSnapshot{}, unsupportedOverride("gpu_devices")
		}
		devices, err := normalizeDevices(*requested.GPUDevices, runtimeFieldChoices(caps, "gpu_devices"))
		if err != nil {
			return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
		}
		if !reflect.DeepEqual(devices, normalizedDevices(instance.GPUDevices)) {
			copy := append([]string(nil), devices...)
			normalized.GPUDevices = &copy
			effective.GPUDevices = copy
			if len(copy) == 0 {
				effective.GPUMode = "auto"
			} else {
				effective.GPUMode = "manual"
			}
		}
	}
	if requested.TensorSplit != nil {
		if !hasRuntimeField(caps, "tensor_split") {
			return RuntimeOverrides{}, InstanceConfigSnapshot{}, unsupportedOverride("tensor_split")
		}
		value, err := normalizeTensorSplit(*requested.TensorSplit)
		if err != nil {
			return RuntimeOverrides{}, InstanceConfigSnapshot{}, err
		}
		if value != strings.TrimSpace(instance.TensorSplit) {
			normalized.TensorSplit = stringPtr(value)
			effective.TensorSplit = value
		}
	}
	if effective.GPUMode == "auto" && requested.GPUDevices != nil && len(effective.GPUDevices) == 0 && requested.TensorSplit == nil {
		// Tensor splits are tied to an explicit device topology. Returning to
		// automatic placement also returns split selection to the scheduler.
		effective.TensorSplit = ""
	}
	if effective.GPUMode == "auto" && strings.TrimSpace(effective.TensorSplit) != "" {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, invalidOverride("tensor_split", "requires explicit GPU devices")
	}
	if effective.GPUMode == "manual" && len(effective.GPUDevices) == 0 {
		return RuntimeOverrides{}, InstanceConfigSnapshot{}, invalidOverride("gpu_devices", "manual GPU placement requires at least one device")
	}
	if effective.TensorSplit != "" && len(effective.GPUDevices) > 0 {
		parts := strings.Split(effective.TensorSplit, ",")
		if len(parts) != len(effective.GPUDevices) {
			return RuntimeOverrides{}, InstanceConfigSnapshot{}, invalidOverride("tensor_split", "must contain one weight per selected GPU")
		}
	}
	return normalized, effective, nil
}
