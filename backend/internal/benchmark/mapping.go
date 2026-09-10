package benchmark

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/scheduler"
)

type MappedConfig struct {
	Args        []string            `json:"args"`
	Differences []MappingDifference `json:"differences,omitempty"`
}

var workloadOwnedOptions = map[string]bool{
	"model": true, "output": true, "repetitions": true, "n-prompt": true, "n-gen": true, "n-depth": true, "pg": true, "no-warmup": true,
}

var placementOwnedOptions = map[string]bool{
	"device": true, "tensor-split": true,
}

var nonBenchmarkOptions = map[string]bool{
	"host": true, "port": true, "metrics": true, "api-key": true, "api-key-file": true,
	"ssl-key-file": true, "ssl-cert-file": true, "timeout": true, "threads-http": true,
	"path": true, "public-path": true, "webui": true, "no-webui": true, "slots": true,
}

var materialRuntimeOptions = map[string]bool{
	"n-gpu-layers": true, "batch-size": true, "ubatch-size": true, "threads": true,
	"cache-type-k": true, "cache-type-v": true, "flash-attn": true, "kv-offload": true,
	"no-kv-offload": true, "split-mode": true, "main-gpu": true, "n-cpu-moe": true,
	"cpu-moe": true, "mmproj": true, "spec-draft-model": true, "mmap": true, "no-mmap": true,
	"mlock": true, "override-tensor": true,
}

func MapInstanceConfig(config InstanceConfigSnapshot, placement scheduler.Placement, capabilities Capabilities) (MappedConfig, error) {
	if !capabilities.Available {
		return MappedConfig{}, fmt.Errorf("%w: %s", ErrUnavailable, capabilities.Reason)
	}
	profile := capabilities.profileForMapping()
	mapped := MappedConfig{}
	keys := make([]string, 0, len(config.Options))
	for key := range config.Options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	for _, original := range keys {
		key := strings.TrimPrefix(strings.TrimSpace(original), "--")
		if key == "" || seen[key] || workloadOwnedOptions[key] || placementOwnedOptions[key] {
			continue
		}
		seen[key] = true
		value := config.Options[original]
		if key == "ctx-size" {
			mapped.Differences = append(mapped.Differences, MappingDifference{Key: key, Value: value, Severity: "info", Reason: "llama-bench workload cases are bounded by the saved context size rather than overriding it"})
			continue
		}
		if key == "kv-offload" && !profile.Has(key) && profile.Has("no-kv-offload") {
			args, err := mappedKVOffloadArgs(profile, value)
			if err != nil {
				return mapped, fmt.Errorf("%w: %v", ErrUnsupportedConfig, err)
			}
			mapped.Args = append(mapped.Args, args...)
			continue
		}
		if profile.Has(key) {
			args, err := mappedOptionArgs(profile, key, value)
			if err != nil {
				return mapped, fmt.Errorf("%w: %v", ErrUnsupportedConfig, err)
			}
			mapped.Args = append(mapped.Args, args...)
			continue
		}
		if nonBenchmarkOptions[key] {
			mapped.Differences = append(mapped.Differences, MappingDifference{Key: key, Value: value, Severity: "ignored", Reason: "server/network option does not participate in llama-bench execution"})
			continue
		}
		source := strings.TrimSpace(config.Sources[original])
		if source == "" {
			source = strings.TrimSpace(config.Sources[key])
		}
		if materialRuntimeOptions[key] || source == "instance" || source == "model" || source == "global" {
			mapped.Differences = append(mapped.Differences, MappingDifference{Key: key, Value: value, Severity: "blocking", Reason: "saved performance-relevant option is not supported by this llama-bench build"})
			return mapped, fmt.Errorf("%w: --%s is not supported by this llama-bench build", ErrUnsupportedConfig, key)
		}
		mapped.Differences = append(mapped.Differences, MappingDifference{Key: key, Value: value, Severity: "ignored", Reason: "manager/detected option is not applicable to the discovered llama-bench interface"})
	}

	if len(placement.Devices) > 0 {
		if !profile.Has("device") {
			mapped.Differences = append(mapped.Differences, MappingDifference{Key: "device", Value: strings.Join(placement.Devices, ","), Severity: "blocking", Reason: "selected benchmark devices cannot be expressed by this llama-bench build"})
			return mapped, fmt.Errorf("%w: selected GPU devices cannot be expressed", ErrUnsupportedConfig)
		}
		// llama-bench uses '/' within one configuration; ',' requests a sweep.
		mapped.Args = append(mapped.Args, "--device", strings.Join(placement.Devices, "/"))
	}
	tensorSplit := strings.TrimSpace(config.TensorSplit)
	if tensorSplit == "" {
		tensorSplit = strings.TrimSpace(placement.TensorSplit)
	}
	if tensorSplit != "" {
		if !profile.Has("tensor-split") {
			mapped.Differences = append(mapped.Differences, MappingDifference{Key: "tensor-split", Value: tensorSplit, Severity: "blocking", Reason: "effective tensor split cannot be expressed by this llama-bench build"})
			return mapped, fmt.Errorf("%w: tensor split cannot be expressed", ErrUnsupportedConfig)
		}
		mapped.Args = append(mapped.Args, "--tensor-split", strings.ReplaceAll(tensorSplit, ",", "/"))
	}
	return mapped, nil
}

func BuildArgv(binaryPath, modelPath string, mapped MappedConfig, workload WorkloadProfile, capabilities Capabilities) ([]string, error) {
	if !capabilities.Available {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, capabilities.Reason)
	}
	if strings.TrimSpace(binaryPath) == "" || strings.TrimSpace(modelPath) == "" {
		return nil, fmt.Errorf("%w: benchmark executable and model path are required", ErrUnsupportedConfig)
	}
	args := []string{binaryPath, "--model", modelPath}
	args = append(args, mapped.Args...)
	// An omitted flag enables llama-bench's default cases. Explicit zero is
	// required to disable the unselected workload family.
	prompt, generation := joinInts(workload.PromptTokens), joinInts(workload.GenerationTokens)
	if prompt == "" {
		prompt = "0"
	}
	if generation == "" {
		generation = "0"
	}
	args = append(args, "--n-prompt", prompt, "--n-gen", generation)
	if len(workload.CombinedCases) > 0 {
		if !capabilities.profileForMapping().HasShort("pg") {
			return nil, fmt.Errorf("%w: this llama-bench build does not support combined prompt-generation workloads", ErrInvalidWorkload)
		}
		for _, combined := range workload.CombinedCases {
			args = append(args, "-pg", fmt.Sprintf("%d,%d", combined.PromptTokens, combined.GenerationTokens))
		}
	}
	if len(workload.ContextDepths) > 0 {
		if !capabilities.profileForMapping().Has("n-depth") {
			return nil, fmt.Errorf("%w: this llama-bench build does not support context-depth workloads", ErrInvalidWorkload)
		}
		args = append(args, "--n-depth", joinInts(workload.ContextDepths))
	}
	args = append(args, "--repetitions", strconv.Itoa(workload.Repetitions), "--output", "json")
	if !workload.Warmup {
		if !capabilities.profileForMapping().Has("no-warmup") {
			return nil, fmt.Errorf("%w: this llama-bench build cannot disable warm-up", ErrInvalidWorkload)
		}
		args = append(args, "--no-warmup")
	}
	return args, nil
}

func mappedKVOffloadArgs(profile llamacpp.Profile, value string) ([]string, error) {
	option, ok := profileOption(profile, "no-kv-offload")
	if !ok || !isBinaryChoiceOption(option) {
		return nil, fmt.Errorf("--kv-offload cannot be represented by this llama-bench build")
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "true", "1", "yes", "on":
		return []string{"--no-kv-offload", "0"}, nil
	case "false", "0", "no", "off":
		return []string{"--no-kv-offload", "1"}, nil
	default:
		return nil, fmt.Errorf("--kv-offload expects a boolean value")
	}
}

func mappedOptionArgs(profile llamacpp.Profile, key, value string) ([]string, error) {
	option, ok := profileOption(profile, key)
	if !ok {
		return nil, fmt.Errorf("unknown llama-bench option --%s", key)
	}
	trimmed := strings.TrimSpace(value)
	// Server switches become explicit values for llama-bench's <0|1> options.
	if isBinaryChoiceOption(option) {
		switch strings.ToLower(trimmed) {
		case "", "true", "1", "yes", "on":
			return []string{"--" + key, "1"}, nil
		case "false", "0", "no", "off":
			return []string{"--" + key, "0"}, nil
		default:
			return nil, fmt.Errorf("--%s expects a boolean value", key)
		}
	}
	if isBooleanBenchmarkOption(option) {
		switch strings.ToLower(trimmed) {
		case "", "true", "1", "yes", "on":
			return []string{"--" + key}, nil
		case "false", "0", "no", "off":
			inverse := inverseBenchmarkBoolean(key)
			inverseOption, exists := profileOption(profile, inverse)
			if !exists || !isBooleanBenchmarkOption(inverseOption) {
				return nil, fmt.Errorf("--%s cannot express explicit false", key)
			}
			return []string{"--" + inverse}, nil
		default:
			return nil, fmt.Errorf("--%s expects a boolean value", key)
		}
	}
	return []string{"--" + key, value}, nil
}

func isBinaryChoiceOption(option llamacpp.Option) bool {
	return len(option.Choices) == 2 && containsChoice(option.Choices, "0") && containsChoice(option.Choices, "1")
}

func containsChoice(choices []string, value string) bool {
	for _, choice := range choices {
		if choice == value {
			return true
		}
	}
	return false
}

func profileOption(profile llamacpp.Profile, key string) (llamacpp.Option, bool) {
	for _, option := range profile.Options {
		if strings.TrimPrefix(strings.TrimSpace(option.Key), "--") == key {
			return option, true
		}
	}
	return llamacpp.Option{}, false
}

func isBooleanBenchmarkOption(option llamacpp.Option) bool {
	if option.Kind != "" {
		return option.Kind == "boolean"
	}
	return strings.TrimSpace(option.ValueHint) == ""
}

func inverseBenchmarkBoolean(key string) string {
	if strings.HasPrefix(key, "no-") {
		return strings.TrimPrefix(key, "no-")
	}
	return "no-" + key
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.Itoa(value)
	}
	return strings.Join(parts, ",")
}
