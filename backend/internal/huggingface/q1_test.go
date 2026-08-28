package huggingface

import "testing"

func TestBonsaiQ1QuantizationDetection(t *testing.T) {
	if got := detectQuantization("Bonsai-27B-Q1_0.gguf"); got != "Q1_0" {
		t.Fatalf("quantization=%q", got)
	}

	artifacts := GroupArtifacts("prism-ml/Bonsai-27B-gguf", "main", []File{{
		Path: "Bonsai-27B-Q1_0.gguf", Size: 3_800_000_000,
	}}, 27_000_000_000)
	if len(artifacts) != 1 || artifacts[0].Quantization != "Q1_0" || !artifacts[0].Complete {
		t.Fatalf("artifacts=%+v", artifacts)
	}
	if artifacts[0].BitsPerWeight <= 0 || artifacts[0].ProfileQuantization() != "Q1_0" {
		t.Fatalf("canonical Q1 profile should outrank derived BPW: %+v", artifacts[0])
	}
}
