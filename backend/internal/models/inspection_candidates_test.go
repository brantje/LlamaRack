package models

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInspectGGUFArtifactCandidatesExposeMetadataClassifiedAlternates(t *testing.T) {
	s, dir := testModelService(t)
	main := writeClassifiedGGUF(t, dir, "model-Q5_K_M.gguf", "qwen2", 0, true)
	projectorF16 := writeClassifiedGGUF(t, dir, "projector-F16.gguf", "clip", 0, false)
	projectorQ8 := writeClassifiedGGUF(t, dir, "projector-Q8_0.gguf", "clip", 0, false)
	draftQ4 := writeClassifiedGGUF(t, dir, "draft-Q4_0.gguf", "qwen35", 1, false)
	draftQ8 := writeClassifiedGGUF(t, dir, "draft-Q8_0.gguf", "qwen35", 1, false)

	candidates, err := s.InspectGGUFArtifactCandidates(context.Background(), main)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 4 {
		t.Fatalf("candidates = %+v", candidates)
	}

	want := map[string]string{
		"mmproj:projector-F16.gguf":  projectorF16,
		"mmproj:projector-Q8_0.gguf": projectorQ8,
		"mtp:draft-Q4_0.gguf":        draftQ4,
		"mtp:draft-Q8_0.gguf":        draftQ8,
	}
	for _, candidate := range candidates {
		key := candidate.Kind + ":" + candidate.Name
		expected, ok := want[key]
		if !ok {
			t.Fatalf("unexpected candidate %+v", candidate)
		}
		if filepath.Clean(candidate.OptionPath) != filepath.Clean(expected) {
			t.Fatalf("candidate %s option path = %q want %q", key, candidate.OptionPath, expected)
		}
		if candidate.TotalBytes <= 0 || len(candidate.Files) == 0 {
			t.Fatalf("candidate %s missing artifact data: %+v", key, candidate)
		}
	}
}

func TestInspectGGUFArtifactCandidatesSkipIncompleteMain(t *testing.T) {
	s, dir := testModelService(t)
	main := writeClassifiedGGUF(t, dir, "partial-Q4_K_M-00001-of-00002.gguf", "qwen2", 0, true)
	_ = writeClassifiedGGUF(t, dir, "projector-F16.gguf", "clip", 0, false)

	candidates, err := s.InspectGGUFArtifactCandidates(context.Background(), main)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("incomplete main candidates = %+v", candidates)
	}
}
