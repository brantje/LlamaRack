package huggingface

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderBPWProfilesCustomGGUFWithoutFilenameGuessing(t *testing.T) {
	const parameters = int64(27_315_000_000)
	artifacts := GroupArtifacts("empero-ai/Qwen3.8-27B-Ridge-GGUF", "rev", []File{
		{Path: "Qwen3.8-27B-Ridge-3.7bpw.gguf", Size: 12_599_187_008},
		{Path: "mmproj-Qwen3.8-27B-BF16.gguf", Size: 931_000_000},
	}, parameters)
	if len(artifacts) != 1 {
		t.Fatalf("artifacts=%+v", artifacts)
	}
	artifact := artifacts[0]
	if detected := detectQuantization(artifact.Name); detected != "" {
		t.Fatalf("custom BPW filename must not be treated as a canonical quantization: %q", detected)
	}
	if artifact.Quantization != "" || artifact.BitsPerWeight != 3.69 || artifact.ProfileQuantization() != "3.69BPW" {
		t.Fatalf("artifact=%+v profile=%q", artifact, artifact.ProfileQuantization())
	}
	if len(artifact.Dependencies) != 1 || artifact.Dependencies[0].Kind != "mmproj" || artifact.Dependencies[0].Quantization != "BF16" {
		t.Fatalf("dependencies=%+v", artifact.Dependencies)
	}

	encoded, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"quantization":"3.69BPW"`) || !strings.Contains(string(encoded), `"bits_per_weight":3.69`) {
		t.Fatalf("json=%s", encoded)
	}
}

func TestProviderBPWDoesNotReplaceExactCanonicalQuantization(t *testing.T) {
	artifacts := GroupArtifacts("acme/demo", "rev", []File{{Path: "demo-Q4_K_M.gguf", Size: 4_800_000_000}}, 8_000_000_000)
	if len(artifacts) != 1 {
		t.Fatalf("artifacts=%+v", artifacts)
	}
	artifact := artifacts[0]
	if artifact.Quantization != "Q4_K_M" || artifact.ProfileQuantization() != "Q4_K_M" || artifact.BitsPerWeight != 4.8 {
		t.Fatalf("artifact=%+v profile=%q", artifact, artifact.ProfileQuantization())
	}
}

func TestProviderGGUFParameterAndBPWFallbacks(t *testing.T) {
	if got := ggufParameterCount(nil); got != 0 {
		t.Fatalf("nil count=%d", got)
	}
	if got := ggufParameterCount(&GGUFInfo{Total: 42}); got != 42 {
		t.Fatalf("total count=%d", got)
	}
	if got := ggufParameterCount(&GGUFInfo{Total: 99, Parameters: map[string]int64{"Q4_K": 10, "Q8_0": 20, "invalid": -1}}); got != 30 {
		t.Fatalf("parameter map count=%d", got)
	}

	for _, tc := range []struct {
		bytes  int64
		params int64
	}{
		{0, 10}, {10, 0}, {-1, 10}, {10, -1}, {1000, 1},
	} {
		if got := artifactBitsPerWeight(tc.bytes, tc.params); got != 0 {
			t.Fatalf("artifactBitsPerWeight(%d,%d)=%v", tc.bytes, tc.params, got)
		}
	}
	if got := (Artifact{}).ProfileQuantization(); got != "" {
		t.Fatalf("empty profile=%q", got)
	}
}
