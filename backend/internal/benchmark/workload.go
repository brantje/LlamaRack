package benchmark

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	maxWorkloadTokens      = 1_000_000
	maxWorkloadRepetitions = 100
)

func DefaultWorkload() WorkloadProfile {
	return WorkloadProfile{
		ID:               DefaultWorkloadID,
		Version:          WorkloadSchemaVersion,
		PromptTokens:     []int{512, 2048},
		GenerationTokens: []int{128},
		Repetitions:      5,
		Warmup:           true,
	}
}

func DefaultWorkloadSchema() WorkloadSchema {
	return WorkloadSchema{
		Version: WorkloadSchemaVersion,
		Default: DefaultWorkload(),
		Fields: []WorkloadField{
			{Key: "prompt_tokens", Label: "Prompt processing", Kind: "integer-list", Minimum: 1, Maximum: maxWorkloadTokens, Description: "Prompt token counts measured as controlled prompt-processing cases."},
			{Key: "generation_tokens", Label: "Generation", Kind: "integer-list", Minimum: 1, Maximum: maxWorkloadTokens, Description: "Generation token counts measured as controlled token-generation cases."},
			{Key: "repetitions", Label: "Repetitions", Kind: "integer", Minimum: 1, Maximum: maxWorkloadRepetitions},
			{Key: "warmup", Label: "Warm up", Kind: "boolean", Advanced: true, Description: "Run llama-bench warm-up before measured repetitions."},
		},
	}
}

func NormalizeWorkload(input *WorkloadProfile) (WorkloadProfile, error) {
	if input == nil || workloadIsZero(*input) {
		return DefaultWorkload(), nil
	}
	workload := *input
	if strings.TrimSpace(workload.ID) == "" {
		workload.ID = DefaultWorkloadID
	}
	if workload.Version == 0 {
		workload.Version = WorkloadSchemaVersion
	}
	if workload.Version != WorkloadSchemaVersion {
		return WorkloadProfile{}, fmt.Errorf("%w: unsupported workload schema version %d", ErrInvalidWorkload, workload.Version)
	}
	if workload.Repetitions < 1 || workload.Repetitions > maxWorkloadRepetitions {
		return WorkloadProfile{}, fmt.Errorf("%w: repetitions must be between 1 and %d", ErrInvalidWorkload, maxWorkloadRepetitions)
	}
	var err error
	workload.PromptTokens, err = normalizeTokenCounts(workload.PromptTokens, "prompt_tokens")
	if err != nil {
		return WorkloadProfile{}, err
	}
	workload.GenerationTokens, err = normalizeTokenCounts(workload.GenerationTokens, "generation_tokens")
	if err != nil {
		return WorkloadProfile{}, err
	}
	if len(workload.PromptTokens) == 0 && len(workload.GenerationTokens) == 0 {
		return WorkloadProfile{}, fmt.Errorf("%w: at least one prompt or generation case is required", ErrInvalidWorkload)
	}
	return workload, nil
}

func ValidateWorkloadContext(workload WorkloadProfile, config InstanceConfigSnapshot) error {
	raw := strings.TrimSpace(config.Options["ctx-size"])
	if raw == "" {
		return nil
	}
	contextSize, err := strconv.Atoi(raw)
	if err != nil || contextSize <= 0 {
		return fmt.Errorf("%w: saved ctx-size %q is invalid", ErrUnsupportedConfig, raw)
	}
	for _, tokens := range append(append([]int(nil), workload.PromptTokens...), workload.GenerationTokens...) {
		if tokens > contextSize {
			return fmt.Errorf("%w: workload case %d tokens exceeds saved context size %d", ErrInvalidWorkload, tokens, contextSize)
		}
	}
	return nil
}

func workloadIsZero(workload WorkloadProfile) bool {
	return workload.ID == "" && workload.Version == 0 && len(workload.PromptTokens) == 0 && len(workload.GenerationTokens) == 0 && workload.Repetitions == 0 && !workload.Warmup
}

func normalizeTokenCounts(values []int, field string) ([]int, error) {
	seen := map[int]bool{}
	out := make([]int, 0, len(values))
	for _, value := range values {
		if value < 1 || value > maxWorkloadTokens {
			return nil, fmt.Errorf("%w: %s values must be between 1 and %d", ErrInvalidWorkload, field, maxWorkloadTokens)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
}
