package recommendations

import (
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
)

func TestClassifyBPWQuantization(t *testing.T) {
	guide := ClassifyQuantization("3.7bpw")
	if !guide.Known || guide.Name != "3.7BPW" || guide.Tier != "Mixed quantization" || guide.Quality != "Recipe-dependent" || guide.Memory != "Low" {
		t.Fatalf("guide=%+v", guide)
	}
	if !strings.Contains(guide.Summary, "3.7 bits per weight") || !strings.Contains(guide.Tradeoff, "not a single quantization method") || guide.Warning == "" {
		t.Fatalf("guide copy=%+v", guide)
	}

	for _, value := range []string{"BPW", "0BPW", "33BPW", "not-bpw"} {
		if got := ClassifyQuantization(value); got.Known {
			t.Fatalf("%q should remain unknown: %+v", value, got)
		}
	}
}

func TestBPWArtifactCanBeRecommended(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	metadata := Metadata{Architecture: "qwen", ContextLength: 262144, BlockCount: 64, Embedding: 5120, HeadCount: 40, KVHeadCount: 8}
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 64 * gib,
		RAMTotalBytes:     64 * gib,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 24 * gib, TotalBytes: 24 * gib}},
	}
	result := AnalyzeDiscover([]ArtifactInput{{
		ID: "ridge", Quantization: "3.7BPW", WeightsBytes: 12_599_187_008, Complete: true,
	}}, metadata, nil, snapshot, 4096, nil, true)

	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts=%+v", result.Artifacts)
	}
	artifact := result.Artifacts[0]
	if !artifact.Quantization.Known || !artifact.Runnable || !artifact.Recommended || artifact.Fit != FitGPU {
		t.Fatalf("artifact=%+v", artifact)
	}
}
