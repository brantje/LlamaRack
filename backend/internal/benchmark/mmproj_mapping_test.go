package benchmark

import (
	"context"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/scheduler"
)

func TestMapInstanceConfigIgnoresMMProjForLlamaBench(t *testing.T) {
	for _, key := range []string{"mmproj", "--mmproj"} {
		t.Run(key, func(t *testing.T) {
			mapped, err := MapInstanceConfig(InstanceConfigSnapshot{
				Options: map[string]string{key: "/models/mmproj-model-f16.gguf"},
				Sources: map[string]string{key: "instance"},
			}, scheduler.Placement{}, testCapabilities())
			if err != nil {
				t.Fatalf("mmproj should not block llama-bench: %v", err)
			}
			if len(mapped.Args) != 0 {
				t.Fatalf("mmproj unexpectedly mapped to llama-bench argv: %v", mapped.Args)
			}
			if len(mapped.Differences) != 1 {
				t.Fatalf("differences = %+v", mapped.Differences)
			}
			difference := mapped.Differences[0]
			if difference.Key != "mmproj" || difference.Severity != "ignored" || !strings.Contains(difference.Reason, "not exercised") {
				t.Fatalf("difference = %+v", difference)
			}
		})
	}
}

func TestBenchmarkServiceAllowsInstanceMMProjWhenBenchDoesNotSupportIt(t *testing.T) {
	executor := benchmarkTestExecutor{output: RunOutput{MachineOutput: []byte(`[{"n_prompt":512,"avg_ts":12}]`)}}
	s, _, _, _, cfg := testBenchmarkService(t, executor)
	cfg.effective.Values["mmproj"] = "model.gguf"
	cfg.effective.Sources["mmproj"] = "instance"

	run, err := s.Create(context.Background(), "instance-1", nil)
	if err != nil {
		t.Fatalf("create benchmark with mmproj: %v", err)
	}
	s.wg.Wait()

	if strings.Contains(strings.Join(run.ResolvedArgv, " "), "mmproj") {
		t.Fatalf("mmproj unexpectedly reached llama-bench argv: %v", run.ResolvedArgv)
	}
	if len(run.MappingDifferences) != 1 || run.MappingDifferences[0].Key != "mmproj" || run.MappingDifferences[0].Severity != "ignored" {
		t.Fatalf("mapping differences = %+v", run.MappingDifferences)
	}
	if run.InstanceConfig.Options["mmproj"] != "model.gguf" {
		t.Fatalf("captured Instance mmproj was lost: %+v", run.InstanceConfig.Options)
	}
}
