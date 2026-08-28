package ggufmeta

import "testing"

func TestGemma4AssistantArchitectureIsStandaloneMTP(t *testing.T) {
	for _, architecture := range []string{"gemma4-assistant", "gemma4_assistant"} {
		t.Run(architecture, func(t *testing.T) {
			path := writeFeatureGGUF(t, architecture, 4, "blk.0.attn_norm.weight", "nextn.pre_projection.weight")

			summary, err := ReadSummary(path)
			if err != nil {
				t.Fatal(err)
			}
			if !summary.Features.HasMTP || !summary.Features.MTPOnly {
				t.Fatalf("summary features = %+v", summary.Features)
			}

			features, err := DetectFeatures(path)
			if err != nil {
				t.Fatal(err)
			}
			if !features.HasMTP || !features.MTPOnly {
				t.Fatalf("detected features = %+v", features)
			}
		})
	}
}

func TestGemma4AssistantArchitectureIsMTPWithoutNextNMetadata(t *testing.T) {
	path := writeFeatureGGUF(t, "gemma4-assistant", 0, "blk.0.attn_norm.weight")
	summary, err := ReadSummary(path)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Features.HasMTP || !summary.Features.MTPOnly {
		t.Fatalf("features = %+v", summary.Features)
	}
}
