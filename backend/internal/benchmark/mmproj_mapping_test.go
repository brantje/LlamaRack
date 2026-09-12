package benchmark

import (
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
