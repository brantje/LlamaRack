package huggingface

import "testing"

func TestGroupArtifactsAssociatesSidecars(t *testing.T) {
	files := []File{
		{Path: "model-Q4_K_M.gguf", Size: 100},
		{Path: "mmproj-F16.gguf", Size: 10},
		{Path: "mmproj-Q8_0.gguf", Size: 12},
		{Path: "mtp-model-Q4_0.gguf", Size: 5},
		{Path: "mtp-model-Q8_0.gguf", Size: 7},
	}
	artifacts := GroupArtifacts("org/repo", "rev", files)
	if len(artifacts) != 1 {
		t.Fatalf("expected one selectable model artifact, got %+v", artifacts)
	}
	artifact := artifacts[0]
	if artifact.Name != "model-Q4_K_M.gguf" || artifact.ModelBytes != 100 || artifact.TotalBytes != 115 {
		t.Fatalf("unexpected artifact sizes: %+v", artifact)
	}
	if len(artifact.Dependencies) != 2 || len(artifact.Files) != 3 {
		t.Fatalf("unexpected dependencies/files: %+v", artifact)
	}
	if artifact.Dependencies[0].Kind != "mmproj" || artifact.Dependencies[0].Name != "mmproj-F16.gguf" {
		t.Fatalf("unexpected projector choice: %+v", artifact.Dependencies[0])
	}
	if artifact.Dependencies[1].Kind != "mtp" || artifact.Dependencies[1].Name != "mtp-model-Q4_0.gguf" {
		t.Fatalf("unexpected MTP choice: %+v", artifact.Dependencies[1])
	}
}

func TestGroupArtifactsPrefersMatchingSidecarQuantization(t *testing.T) {
	artifacts := GroupArtifacts("org/repo", "rev", []File{
		{Path: "model-Q8_0.gguf", Size: 100},
		{Path: "mmproj-F16.gguf", Size: 10},
		{Path: "mmproj-Q8_0.gguf", Size: 12},
		{Path: "mtp-model-Q4_0.gguf", Size: 5},
		{Path: "mtp-model-Q8_0.gguf", Size: 7},
	})
	if len(artifacts) != 1 || len(artifacts[0].Dependencies) != 2 {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}
	if artifacts[0].Dependencies[0].Name != "mmproj-Q8_0.gguf" || artifacts[0].Dependencies[1].Name != "mtp-model-Q8_0.gguf" {
		t.Fatalf("expected quantization-matched helpers: %+v", artifacts[0].Dependencies)
	}
	if artifacts[0].TotalBytes != 119 {
		t.Fatalf("total bytes = %d", artifacts[0].TotalBytes)
	}
}

func TestSidecarClassificationPreservesProjectorRulesAndConservativeMTP(t *testing.T) {
	for _, name := range []string{
		"mmproj-F16.gguf", "mmoproj_model.gguf", "projector.vision.gguf",
		"asda-projector-Q4_K_M.gguf", "multimodal-mmproj-compatible.gguf", "vision/projector/model.gguf",
	} {
		if got := sidecarKind(name); got != "mmproj" {
			t.Fatalf("sidecarKind(%q) = %q", name, got)
		}
	}
	for _, name := range []string{"mtp-model-Q4_0.gguf", "MTP_Q8_0.GGUF"} {
		if got := sidecarKind(name); got != "mtp" {
			t.Fatalf("sidecarKind(%q) = %q", name, got)
		}
	}
	if got := sidecarKind("model-MTP-Q4_K_M.gguf"); got != "" {
		t.Fatalf("embedded-MTP main model classified as %q", got)
	}
}

func TestIncompleteSidecarSplitIsNotAttached(t *testing.T) {
	artifacts := GroupArtifacts("org/repo", "rev", []File{
		{Path: "model-Q4_K_M.gguf", Size: 100},
		{Path: "mmproj-F16-00001-of-00002.gguf", Size: 10},
	})
	if len(artifacts) != 1 || len(artifacts[0].Dependencies) != 0 || artifacts[0].TotalBytes != 100 {
		t.Fatalf("incomplete helper must not be attached: %+v", artifacts)
	}
}
