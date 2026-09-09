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

func WorkloadPresets() []WorkloadProfile {
	return []WorkloadProfile{
		{
			ID:          DefaultWorkloadID,
			Version:     WorkloadSchemaVersion,
			Name:        "Balanced",
			Description: "General-purpose mix of prompt processing and generation for typical local LLM use.",
			Focus:       "Balance prompt ingestion and token generation rather than optimizing only one phase.",
			TuningHints: balancedTuningHints(),
			PromptTokens:     []int{512, 2048},
			GenerationTokens: []int{128},
			Repetitions:      5,
			Warmup:           true,
		},
		{
			ID:          "interactive-v1",
			Version:     WorkloadSchemaVersion,
			Name:        "Interactive / chat",
			Description: "Short-to-medium prompts with short generation cases representative of interactive chat and assistants.",
			Focus:       "Prioritize generation responsiveness while keeping prompt processing visible.",
			TuningHints: generationTuningHints(),
			PromptTokens:     []int{256, 1024},
			GenerationTokens: []int{32, 128},
			Repetitions:      5,
			Warmup:           true,
		},
		{
			ID:          "prompt-heavy-v1",
			Version:     WorkloadSchemaVersion,
			Name:        "Prompt-heavy / RAG",
			Description: "Larger prompt-processing cases for document ingestion, RAG context and other prefill-heavy workloads.",
			Focus:       "Maximize prompt-processing throughput without hiding generation regressions.",
			TuningHints: promptTuningHints(),
			PromptTokens:     []int{1024, 2048},
			GenerationTokens: []int{128},
			Repetitions:      5,
			Warmup:           true,
		},
		{
			ID:          "long-prompt-v1",
			Version:     WorkloadSchemaVersion,
			Name:        "Long prompt ingestion",
			Description: "Large prompt-processing cases for Instances intended to ingest long documents or large retrieved contexts.",
			Focus:       "Measure sustained prefill throughput near common 4K context sizes.",
			TuningHints: promptTuningHints(),
			PromptTokens:     []int{2048, 4096},
			GenerationTokens: []int{128},
			Repetitions:      5,
			Warmup:           true,
		},
		{
			ID:          "generation-heavy-v1",
			Version:     WorkloadSchemaVersion,
			Name:        "Generation-heavy",
			Description: "Longer token-generation cases for coding, writing and offline generation workloads.",
			Focus:       "Maximize sustained token generation while retaining a small prompt-processing baseline.",
			TuningHints: generationTuningHints(),
			PromptTokens:     []int{512},
			GenerationTokens: []int{128, 512},
			Repetitions:      5,
			Warmup:           true,
		},
	}
}

func DefaultWorkload() WorkloadProfile {
	profile, _ := workloadPreset(DefaultWorkloadID)
	return profile
}

func DefaultWorkloadSchema() WorkloadSchema {
	return WorkloadSchema{
		Version: WorkloadSchemaVersion,
		Default: DefaultWorkload(),
		Presets: WorkloadPresets(),
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
	workload.ID = strings.TrimSpace(workload.ID)
	if workload.ID != "" && workloadDimensionsEmpty(workload) {
		if preset, ok := workloadPreset(workload.ID); ok {
			return preset, nil
		}
	}
	if workload.ID == "" {
		// Preserve the v1 API behavior for callers that supplied explicit fields
		// without an ID. The resolved fields remain authoritative in history.
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
	return decorateWorkload(workload), nil
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

func workloadPreset(id string) (WorkloadProfile, bool) {
	for _, preset := range WorkloadPresets() {
		if preset.ID == strings.TrimSpace(id) {
			return preset, true
		}
	}
	return WorkloadProfile{}, false
}

func decorateWorkload(workload WorkloadProfile) WorkloadProfile {
	if preset, ok := workloadPreset(workload.ID); ok && sameWorkloadShape(workload, preset) {
		workload.Name = preset.Name
		workload.Description = preset.Description
		workload.Focus = preset.Focus
		workload.TuningHints = preset.TuningHints
		return workload
	}
	workload.Name = "Custom"
	workload.Description = "User-defined controlled llama-bench workload."
	workload.Focus = "Use comparison runs to determine which saved Instance settings matter for these cases."
	switch {
	case len(workload.PromptTokens) > 0 && len(workload.GenerationTokens) == 0:
		workload.TuningHints = promptTuningHints()
	case len(workload.GenerationTokens) > 0 && len(workload.PromptTokens) == 0:
		workload.TuningHints = generationTuningHints()
	default:
		workload.TuningHints = balancedTuningHints()
	}
	return workload
}

func workloadDimensionsEmpty(workload WorkloadProfile) bool {
	return len(workload.PromptTokens) == 0 && len(workload.GenerationTokens) == 0 && workload.Repetitions == 0
}

func sameWorkloadShape(left, right WorkloadProfile) bool {
	if left.Repetitions != right.Repetitions || left.Warmup != right.Warmup || !intSlicesEqual(left.PromptTokens, right.PromptTokens) || !intSlicesEqual(left.GenerationTokens, right.GenerationTokens) {
		return false
	}
	return true
}

func intSlicesEqual(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
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

func balancedTuningHints() []TuningHint {
	return []TuningHint{
		{Key: "n-gpu-layers", Impact: "Prompt + generation", Reason: "GPU offload can improve both phases when the model and working set fit available VRAM."},
		{Key: "tensor-split", Impact: "Multi-GPU placement", Reason: "On multi-GPU systems, a better split can reduce imbalance and transfer overhead."},
		{Key: "flash-attn", Impact: "Prompt + memory", Reason: "Flash attention can change prompt throughput and memory pressure, especially as context grows."},
		{Key: "batch-size", Impact: "Prompt processing", Reason: "Prompt processing is commonly sensitive to the logical batch size."},
		{Key: "ubatch-size", Impact: "Prompt processing", Reason: "Micro-batch size trades prompt throughput against working-memory pressure."},
		{Key: "threads", Impact: "CPU / hybrid", Reason: "CPU and hybrid runs can have a clear thread-count sweet spot instead of scaling indefinitely."},
	}
}

func promptTuningHints() []TuningHint {
	return []TuningHint{
		{Key: "batch-size", Impact: "Prompt processing", Reason: "Larger logical batches can raise prefill throughput until memory or backend limits dominate."},
		{Key: "ubatch-size", Impact: "Prompt processing", Reason: "Micro-batch size is a direct throughput-versus-memory-pressure tuning knob for prompt ingestion."},
		{Key: "flash-attn", Impact: "Prompt + memory", Reason: "Flash attention can materially affect attention-heavy prompt processing and memory use."},
		{Key: "n-gpu-layers", Impact: "Prompt processing", Reason: "Additional GPU offload can reduce CPU work when sufficient VRAM is available."},
		{Key: "tensor-split", Impact: "Multi-GPU placement", Reason: "Prompt-heavy workloads can expose poor load balance between selected GPUs."},
		{Key: "threads", Impact: "CPU / hybrid", Reason: "Thread count is important when prompt processing still performs meaningful CPU work."},
	}
}

func generationTuningHints() []TuningHint {
	return []TuningHint{
		{Key: "n-gpu-layers", Impact: "Generation", Reason: "Token generation benefits strongly from keeping repeated model work on the fastest available device when VRAM permits."},
		{Key: "tensor-split", Impact: "Multi-GPU generation", Reason: "A balanced split can improve generation when the model spans multiple GPUs."},
		{Key: "cache-type-k", Impact: "Generation + KV memory", Reason: "KV cache precision changes memory pressure and can change generation throughput."},
		{Key: "cache-type-v", Impact: "Generation + KV memory", Reason: "KV cache precision changes memory pressure and can change generation throughput."},
		{Key: "kv-offload", Impact: "Generation", Reason: "Keeping KV operations on the accelerator can avoid host-device overhead when supported."},
		{Key: "flash-attn", Impact: "Generation + memory", Reason: "Flash attention can alter attention cost and memory use as the active context grows."},
		{Key: "threads", Impact: "CPU / hybrid", Reason: "CPU and hybrid generation often has a thread-count optimum that should be measured."},
		{Key: "n-cpu-moe", Impact: "MoE placement", Reason: "For MoE models, expert placement can trade VRAM use against CPU and transfer cost."},
	}
}
