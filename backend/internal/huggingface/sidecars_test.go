package huggingface

import (
	"strings"
	"testing"
)

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
	for _, name := range []string{
		"mtp-model-Q4_0.gguf", "MTP_Q8_0.GGUF",
		"MTP/gemma-4-12B-it-MTP-BF16.gguf", "helpers/mtp/draft.gguf",
	} {
		if got := sidecarKind(name); got != "mtp" {
			t.Fatalf("sidecarKind(%q) = %q", name, got)
		}
	}
	if got := sidecarKind("model-MTP-Q4_K_M.gguf"); got != "" {
		t.Fatalf("embedded-MTP main model classified as %q", got)
	}
	if got := sidecarKind("mtp-drafts/model-Q4_K_M.gguf"); got != "" {
		t.Fatalf("non-mtp directory classified as %q", got)
	}
}

func TestGroupArtifactsTreatsMTPDirectoryAsSidecar(t *testing.T) {
	artifacts := GroupArtifacts("yuxinlu1/gemma-4-12B-agentic-fable5-composer2.5-v2-3.5x-tau2-GGUF", "rev", []File{
		{Path: "MTP/gemma-4-12B-it-MTP-BF16.gguf", Size: 861520128},
		{Path: "MTP/gemma-4-12B-it-MTP-F16.gguf", Size: 861520128},
		{Path: "MTP/gemma-4-12B-it-MTP-Q8_0.gguf", Size: 465109248},
		{Path: "gemma4-v2-Q3_K_M.gguf", Size: 6087086624},
		{Path: "gemma4-v2-Q4_K_M.gguf", Size: 7381381664},
		{Path: "gemma4-v2-Q6_K.gguf", Size: 9786020384},
		{Path: "gemma4-v2-Q8_0.gguf", Size: 12669645344},
	})
	if len(artifacts) != 4 {
		t.Fatalf("expected four main quants, got %+v", artifacts)
	}
	for _, artifact := range artifacts {
		if strings.Contains(strings.ToLower(artifact.Name), "mtp") {
			t.Fatalf("MTP draft listed as main artifact: %+v", artifact)
		}
		if len(artifact.Dependencies) != 1 || artifact.Dependencies[0].Kind != "mtp" {
			t.Fatalf("expected attached MTP draft, got %+v", artifact)
		}
	}
	q8 := artifacts[0]
	for _, artifact := range artifacts {
		if artifact.Quantization == "Q8_0" {
			q8 = artifact
			break
		}
	}
	if q8.Quantization != "Q8_0" || q8.Dependencies[0].Name != "gemma-4-12B-it-MTP-Q8_0.gguf" {
		t.Fatalf("Q8_0 should attach matching MTP quant: %+v", q8)
	}
	q4 := artifacts[0]
	for _, artifact := range artifacts {
		if artifact.Quantization == "Q4_K_M" {
			q4 = artifact
			break
		}
	}
	if q4.Dependencies[0].Name != "gemma-4-12B-it-MTP-Q8_0.gguf" {
		t.Fatalf("unmatched main quant should fall back to Q8_0 MTP: %+v", q4)
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
