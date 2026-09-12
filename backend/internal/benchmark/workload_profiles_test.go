package benchmark

import (
	"errors"
	"testing"
)

func TestWorkloadPresetCatalog(t *testing.T) {
	presets := WorkloadPresets()
	if len(presets) < 5 {
		t.Fatalf("expected workload intent presets, got %d", len(presets))
	}
	seen := map[string]bool{}
	for _, preset := range presets {
		if preset.ID == "" || preset.Name == "" || preset.Description == "" || preset.Focus == "" {
			t.Fatalf("incomplete preset metadata: %+v", preset)
		}
		if seen[preset.ID] {
			t.Fatalf("duplicate preset id %q", preset.ID)
		}
		seen[preset.ID] = true
		if len(preset.PromptTokens) == 0 && len(preset.GenerationTokens) == 0 {
			t.Fatalf("preset %q has no cases", preset.ID)
		}
		if len(preset.TuningHints) == 0 {
			t.Fatalf("preset %q has no tuning guidance", preset.ID)
		}
	}
	for _, id := range []string{DefaultWorkloadID, "interactive-v1", "prompt-heavy-v1", "long-prompt-v1", "generation-heavy-v1"} {
		if !seen[id] {
			t.Fatalf("missing preset %q", id)
		}
	}

	schema := DefaultWorkloadSchema()
	if len(schema.Presets) != len(presets) || schema.Default.ID != DefaultWorkloadID {
		t.Fatalf("schema=%+v", schema)
	}
}

func TestNormalizeWorkloadResolvesPresetByID(t *testing.T) {
	workload, err := NormalizeWorkload(&WorkloadProfile{ID: "generation-heavy-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if workload.Name != "Generation-heavy" || len(workload.GenerationTokens) != 2 || workload.GenerationTokens[1] != 512 {
		t.Fatalf("resolved workload=%+v", workload)
	}
	if len(workload.TuningHints) == 0 || workload.TuningHints[0].Key != "n-gpu-layers" {
		t.Fatalf("tuning hints=%+v", workload.TuningHints)
	}
}

func TestNormalizeWorkloadDecoratesCustomResolvedFields(t *testing.T) {
	workload, err := NormalizeWorkload(&WorkloadProfile{
		PromptTokens:     []int{256},
		GenerationTokens: []int{64},
		Repetitions:      3,
		Warmup:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Keep the existing v1 ID fallback for API compatibility while making the
	// resolved metadata honest about the modified workload.
	if workload.ID != DefaultWorkloadID || workload.Name != "Custom" || len(workload.TuningHints) == 0 {
		t.Fatalf("custom workload=%+v", workload)
	}
}

func TestLongPromptPresetRespectsSavedContext(t *testing.T) {
	workload, err := NormalizeWorkload(&WorkloadProfile{ID: "long-prompt-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkloadContext(workload, InstanceConfigSnapshot{Options: map[string]string{"ctx-size": "2048"}}); !errors.Is(err, ErrInvalidWorkload) {
		t.Fatalf("expected context validation error, got %v", err)
	}
	if err := ValidateWorkloadContext(workload, InstanceConfigSnapshot{Options: map[string]string{"ctx-size": "4096"}}); err != nil {
		t.Fatal(err)
	}
}
