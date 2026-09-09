package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
)

const (
	BenchmarkSchemaVersion      = 1
	ParserSchemaVersion         = 1
	ConfigSchemaVersion         = 1
	LegacyWorkloadSchemaVersion = 1
	WorkloadSchemaVersion       = 2
	DefaultWorkloadID           = "standard-v1"
	CustomWorkloadID            = "custom-v1"
)

type Status string

const (
	StatusQueued    Status = "QUEUED"
	StatusRunning   Status = "RUNNING"
	StatusCompleted Status = "COMPLETED"
	StatusFailed    Status = "FAILED"
	StatusCancelled Status = "CANCELLED"
)

var (
	ErrNotFound              = errors.New("benchmark run not found")
	ErrTransitionConflict    = errors.New("benchmark run state transition conflict")
	ErrInvalidWorkload       = errors.New("invalid benchmark workload")
	ErrUnavailable           = errors.New("benchmarking unavailable")
	ErrUnsupportedConfig     = errors.New("instance configuration is not safely representable by llama-bench")
	ErrInsufficientResources = errors.New("insufficient resources for benchmark")
)

func (s Status) Valid() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

func (s Status) Terminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

func CanTransition(from, to Status) bool {
	switch from {
	case StatusQueued:
		return to == StatusRunning || to == StatusFailed || to == StatusCancelled
	case StatusRunning:
		return to == StatusCompleted || to == StatusFailed || to == StatusCancelled
	default:
		return false
	}
}

type TuningHint struct {
	Key    string `json:"key"`
	Impact string `json:"impact"`
	Reason string `json:"reason"`
}

type WorkloadCombinedCase struct {
	PromptTokens     int `json:"prompt_tokens"`
	GenerationTokens int `json:"generation_tokens"`
}

type WorkloadProfile struct {
	ID               string                 `json:"id"`
	Version          int                    `json:"version"`
	Name             string                 `json:"name,omitempty"`
	Description      string                 `json:"description,omitempty"`
	Focus            string                 `json:"focus,omitempty"`
	TuningHints      []TuningHint           `json:"tuning_hints,omitempty"`
	PromptTokens     []int                  `json:"prompt_tokens"`
	GenerationTokens []int                  `json:"generation_tokens"`
	CombinedCases    []WorkloadCombinedCase `json:"combined_cases,omitempty"`
	ContextDepths    []int                  `json:"context_depths,omitempty"`
	Repetitions      int                    `json:"repetitions"`
	Warmup           bool                   `json:"warmup"`
}

type InstanceConfigSnapshot struct {
	SchemaVersion int               `json:"schema_version"`
	GPUMode       string            `json:"gpu_mode"`
	GPUDevices    []string          `json:"gpu_devices,omitempty"`
	TensorSplit   string            `json:"tensor_split,omitempty"`
	Options       map[string]string `json:"options"`
	Sources       map[string]string `json:"sources,omitempty"`
}

type ArtifactFileSnapshot struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type ArtifactDependencySnapshot struct {
	Kind         string                 `json:"kind"`
	Name         string                 `json:"name"`
	Quantization string                 `json:"quantization,omitempty"`
	Files        []ArtifactFileSnapshot `json:"files"`
}

type ArtifactSnapshot struct {
	Path           string                       `json:"path"`
	Fingerprint    string                       `json:"fingerprint"`
	Size           int64                        `json:"size"`
	Quantization   string                       `json:"quantization,omitempty"`
	Architecture   string                       `json:"architecture,omitempty"`
	ShardCount     int                          `json:"shard_count"`
	ExpectedShards int                          `json:"expected_shards"`
	Files          []ArtifactFileSnapshot       `json:"files"`
	Dependencies   []ArtifactDependencySnapshot `json:"dependencies,omitempty"`
}

type CPUSnapshot struct {
	Model            string `json:"model,omitempty"`
	LogicalThreads   int    `json:"logical_threads"`
	EffectiveThreads int    `json:"effective_threads,omitempty"`
	Architecture     string `json:"architecture"`
	OS               string `json:"os"`
}

type HardwareSnapshot struct {
	Observed        hardware.Snapshot `json:"observed"`
	CPU             CPUSnapshot       `json:"cpu"`
	SelectedDevices []string          `json:"selected_devices,omitempty"`
}

type BuildSnapshot struct {
	LlamaRackVersion      string `json:"llamarack_version"`
	LlamaRackCommit       string `json:"llamarack_commit,omitempty"`
	RuntimeVariant        string `json:"runtime_variant,omitempty"`
	LlamaCppRelease       string `json:"llama_cpp_release,omitempty"`
	LlamaCppBuild         string `json:"llama_cpp_build,omitempty"`
	LlamaBenchVersion     string `json:"llama_bench_version,omitempty"`
	LlamaBenchFingerprint string `json:"llama_bench_fingerprint,omitempty"`
}

type MappingDifference struct {
	Key      string `json:"key"`
	Value    string `json:"value,omitempty"`
	Severity string `json:"severity"`
	Reason   string `json:"reason"`
}

type Result struct {
	CaseIndex        int             `json:"case_index"`
	CaseID           string          `json:"case_id"`
	PromptTokens     int64           `json:"prompt_tokens"`
	GenerationTokens int64           `json:"generation_tokens"`
	ContextDepth     int64           `json:"context_depth,omitempty"`
	Repetitions      int             `json:"repetitions"`
	AverageNS        int64           `json:"average_ns,omitempty"`
	StdDevNS         int64           `json:"stddev_ns,omitempty"`
	AverageTokensPS  float64         `json:"average_tokens_per_second,omitempty"`
	StdDevTokensPS   float64         `json:"stddev_tokens_per_second,omitempty"`
	RawFields        json.RawMessage `json:"raw_fields"`
}

type Run struct {
	ID string `json:"id"`

	InstanceID           string                 `json:"instance_id"`
	InstanceSlugSnapshot string                 `json:"instance_slug_snapshot"`
	InstanceNameSnapshot string                 `json:"instance_name_snapshot"`
	InstanceConfig       InstanceConfigSnapshot `json:"instance_config_snapshot"`

	ModelID           string           `json:"model_id"`
	ModelSlugSnapshot string           `json:"model_slug_snapshot"`
	ModelNameSnapshot string           `json:"model_name_snapshot"`
	Artifact          ArtifactSnapshot `json:"artifact_snapshot"`

	Workload           WorkloadProfile     `json:"workload_profile"`
	ResolvedArgv       []string            `json:"resolved_argv"`
	MappingDifferences []MappingDifference `json:"mapping_differences,omitempty"`
	Status             Status              `json:"status"`

	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	Build    BuildSnapshot    `json:"build"`
	Hardware HardwareSnapshot `json:"hardware_snapshot"`

	BenchmarkSchemaVersion int      `json:"benchmark_schema_version"`
	ParserSchemaVersion    int      `json:"parser_schema_version"`
	Failure                string   `json:"failure,omitempty"`
	DiagnosticOutput       string   `json:"diagnostic_output,omitempty"`
	Results                []Result `json:"results,omitempty"`
}

type Filter struct {
	InstanceID string
	ModelID    string
	Status     Status
	Limit      int
	Offset     int
}

type Page struct {
	Items  []Run `json:"items"`
	Total  int   `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

type TransitionUpdate struct {
	StartedAt        *time.Time
	CompletedAt      *time.Time
	Failure          string
	DiagnosticOutput string
}

type Completion struct {
	CompletedAt      time.Time
	DiagnosticOutput string
}

type Store interface {
	CreateRun(ctx context.Context, run Run) error
	GetRun(ctx context.Context, id string) (Run, error)
	ListRuns(ctx context.Context, filter Filter) (Page, error)
	TransitionRun(ctx context.Context, id string, from, to Status, update TransitionUpdate) (Run, error)
	CompleteRun(ctx context.Context, id string, completion Completion, results []Result) (Run, error)
	DeleteRun(ctx context.Context, id string) error
}
