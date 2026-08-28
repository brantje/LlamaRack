package huggingface

import "testing"

func TestDetectBPWQuantization(t *testing.T) {
	cases := map[string]string{
		"Qwen3.8-27B-Ridge-3.7bpw.gguf": "3.7BPW",
		"model-4bpw.gguf":                 "4BPW",
		"model-Q4_K_M.gguf":               "Q4_K_M",
		"model.gguf":                      "",
	}
	for name, want := range cases {
		if got := detectQuantization(name); got != want {
			t.Fatalf("detectQuantization(%q)=%q want %q", name, got, want)
		}
	}
}

func TestGroupArtifactsPreservesBPWProfileAndSidecar(t *testing.T) {
	artifacts := GroupArtifacts("empero-ai/Qwen3.8-27B-Ridge-GGUF", "rev", []File{
		{Path: "Qwen3.8-27B-Ridge-3.7bpw.gguf", Size: 12_599_187_008},
		{Path: "mmproj-Qwen3.8-27B-BF16.gguf", Size: 931_000_000},
	})
	if len(artifacts) != 1 {
		t.Fatalf("artifacts=%+v", artifacts)
	}
	artifact := artifacts[0]
	if artifact.Quantization != "3.7BPW" || !artifact.Complete {
		t.Fatalf("artifact=%+v", artifact)
	}
	if len(artifact.Dependencies) != 1 || artifact.Dependencies[0].Kind != "mmproj" || artifact.Dependencies[0].Quantization != "BF16" {
		t.Fatalf("dependencies=%+v", artifact.Dependencies)
	}
}
