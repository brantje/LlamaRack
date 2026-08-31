package recommendations

import (
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
)

func TestQ1DiscoverProfileIsKnownAndRunnable(t *testing.T) {
	guide := ClassifyQuantization("Q1_0")
	if !guide.Known || guide.Name != "Q1_0" || guide.Tier != "Extreme compression" || guide.Quality != "Model-dependent" || guide.Memory != "Minimal" {
		t.Fatalf("guide=%+v", guide)
	}
	if guide.rank != 10 || !strings.Contains(guide.Warning, "specialized") || !strings.Contains(guide.Summary, "One-bit") {
		t.Fatalf("guide=%+v", guide)
	}

	low, high := quantizationBandwidthEfficiency("Q1_0")
	if low != 0.28 || high != 0.52 {
		t.Fatalf("Q1 efficiency=%v %v", low, high)
	}

	const gib = int64(1024 * 1024 * 1024)
	metadata := Metadata{
		Architecture: "qwen35", ContextLength: 262144, BlockCount: 64,
		Embedding: 5120, HeadCount: 40, KVHeadCount: 8,
	}
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 32 * gib,
		RAMTotalBytes:     64 * gib,
		GPUs: []hardware.GPU{{
			ID: "CUDA0", FreeBytes: 16 * gib, TotalBytes: 16 * gib,
			MemoryBandwidthBytesPerSecond: 288_000_000_000,
		}},
	}
	result := AnalyzeDiscover([]ArtifactInput{{
		ID: "bonsai-q1", Quantization: "Q1_0", WeightsBytes: 4 * gib, Complete: true,
	}}, metadata, nil, snapshot, 4096, nil, true, true)
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts=%+v", result.Artifacts)
	}
	artifact := result.Artifacts[0]
	if !artifact.Recommended || !artifact.Runnable || artifact.Fit != FitGPU || !artifact.EstimatedGenerationSpeed.Estimated {
		t.Fatalf("artifact=%+v", artifact)
	}
}

func TestQ1DoesNotOutrankHigherBitRunnableQuantization(t *testing.T) {
	const gib = int64(1024 * 1024 * 1024)
	metadata := Metadata{Architecture: "qwen35", ContextLength: 32768, BlockCount: 64, Embedding: 5120, HeadCount: 40, KVHeadCount: 8}
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 32 * gib,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 16 * gib, TotalBytes: 16 * gib}},
	}
	result := AnalyzeDiscover([]ArtifactInput{
		{ID: "q1", Quantization: "Q1_0", WeightsBytes: 4 * gib, Complete: true},
		{ID: "q2", Quantization: "Q2_K", WeightsBytes: 6 * gib, Complete: true},
	}, metadata, nil, snapshot, 4096, nil, true, true)
	if len(result.Artifacts) != 2 || result.Artifacts[0].ArtifactID != "q2" || !result.Artifacts[0].Recommended {
		t.Fatalf("artifacts=%+v", result.Artifacts)
	}
}
