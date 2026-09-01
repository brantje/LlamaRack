package recommendations

import (
	"errors"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
)

func TestClassifyQuantizationForNoviceGuidance(t *testing.T) {
	cases := []struct {
		quantization string
		tier         string
		quality      string
		memory       string
		warning      bool
		known        bool
	}{
		{"Q2_K", "Very compact", "Very low", "Very low", true, true},
		{"IQ3_XS", "Compact", "Low", "Low", true, true},
		{"Q4_K_M", "Balanced", "Balanced", "Moderate", false, true},
		{"Q5_K_M", "High quality", "High", "Moderate-high", false, true},
		{"Q6_K_P", "High quality", "High", "High", false, true},
		{"Q8_0", "Maximum quality", "Maximum", "Very high", true, true},
		{"F16", "Maximum quality", "Maximum", "Extreme", true, true},
		{"BF16", "Maximum quality", "Maximum", "Extreme", true, true},
		{"F32", "Maximum quality", "Maximum", "Extreme", true, true},
		{"EXPERIMENTAL", "Unknown profile", "Unknown", "Unknown", false, false},
	}
	for _, tc := range cases {
		guide := ClassifyQuantization(tc.quantization)
		if guide.Tier != tc.tier || guide.Quality != tc.quality || guide.Memory != tc.memory || guide.Known != tc.known || (guide.Warning != "") != tc.warning {
			t.Fatalf("%s => %+v", tc.quantization, guide)
		}
	}
}

func TestAnalyzeDiscoverContextAndHybridPolicy(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	metadata := Metadata{Architecture: "qwen2", ContextLength: 131072, BlockCount: 32, Embedding: 4096, HeadCount: 32, KVHeadCount: 8}
	inputs := []ArtifactInput{
		{ID: "q4", Quantization: "Q4_K_M", WeightsBytes: 4 * gib, Complete: true},
		{ID: "q6", Quantization: "Q6_K_P", WeightsBytes: 6 * gib, Complete: true},
		{ID: "q8", Quantization: "Q8_0", WeightsBytes: 8 * gib, Complete: true},
	}
	snapshot := hardware.Snapshot{RAMAvailableBytes: 32 * gib, RAMTotalBytes: 48 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib, TotalBytes: 8 * gib}}}

	allowed := AnalyzeDiscover(inputs, metadata, nil, snapshot, 4096, nil, true, true)
	if allowed.ContextAssumed || allowed.ContextLength != 4096 || allowed.ContextCapability != 131072 || !allowed.HardwareAvailable {
		t.Fatalf("analysis=%+v", allowed)
	}
	if len(allowed.Artifacts) != 3 || allowed.Artifacts[0].ArtifactID != "q8" || !allowed.Artifacts[0].Recommended || allowed.Artifacts[0].Fit != FitHybrid {
		t.Fatalf("hybrid-enabled=%+v", allowed.Artifacts)
	}

	gpuPreferred := AnalyzeDiscover(inputs, metadata, nil, snapshot, 4096, nil, false, true)
	if gpuPreferred.Artifacts[0].ArtifactID != "q6" || !gpuPreferred.Artifacts[0].Recommended || gpuPreferred.Artifacts[0].Fit != FitGPU {
		t.Fatalf("hybrid-disabled=%+v", gpuPreferred.Artifacts)
	}

	largeContext := AnalyzeDiscover(inputs, metadata, nil, snapshot, 65536, nil, false, true)
	var q6 DiscoverArtifact
	for _, artifact := range largeContext.Artifacts {
		if artifact.ArtifactID == "q6" { q6 = artifact }
	}
	if q6.Memory.KVCacheBytes <= allowed.Artifacts[1].Memory.KVCacheBytes || q6.Fit == FitGPU {
		t.Fatalf("large-context q6=%+v", q6)
	}
}

func TestAnalyzeDiscoverFitStatesAndUnknowns(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	metadata := Metadata{Architecture: "llama", ContextLength: 32768, BlockCount: 32, Embedding: 4096, HeadCount: 32, KVHeadCount: 8}
	input := []ArtifactInput{{ID: "q4", Quantization: "Q4_K_M", WeightsBytes: 4 * gib, Complete: true}}

	multi := AnalyzeDiscover(input, metadata, nil, hardware.Snapshot{RAMAvailableBytes: 16 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 3 * gib}, {ID: "CUDA1", FreeBytes: 3 * gib}}}, 4096, nil, true, true)
	if multi.Artifacts[0].Fit != FitMultiGPU { t.Fatalf("multi=%+v", multi.Artifacts[0]) }

	cpu := AnalyzeDiscover(input, metadata, nil, hardware.Snapshot{RAMAvailableBytes: 16 * gib, RAMTotalBytes: 32 * gib}, 4096, nil, true, true)
	if cpu.Artifacts[0].Fit != FitCPU || !cpu.Artifacts[0].Runnable { t.Fatalf("cpu=%+v", cpu.Artifacts[0]) }

	noFit := AnalyzeDiscover(input, metadata, nil, hardware.Snapshot{RAMAvailableBytes: 2 * gib, RAMTotalBytes: 2 * gib}, 4096, nil, true, true)
	if noFit.Artifacts[0].Fit != FitNo || noFit.Artifacts[0].Runnable { t.Fatalf("no-fit=%+v", noFit.Artifacts[0]) }

	noHardware := AnalyzeDiscover(input, metadata, nil, hardware.Snapshot{}, 4096, errors.New("telemetry failed"), true, true)
	if noHardware.HardwareAvailable || noHardware.Artifacts[0].Fit != FitUnknown { t.Fatalf("no-hardware=%+v", noHardware) }

	missingMetadata := AnalyzeDiscover(input, Metadata{}, errors.New("metadata unavailable"), hardware.Snapshot{RAMAvailableBytes: 16 * gib}, 0, nil, true, true)
	if !missingMetadata.ContextAssumed || missingMetadata.Artifacts[0].Fit != FitUnknown || missingMetadata.Artifacts[0].Recommended { t.Fatalf("missing-metadata=%+v", missingMetadata) }

	incomplete := AnalyzeDiscover([]ArtifactInput{{ID: "split", Quantization: "Q6_K", WeightsBytes: 4 * gib, Complete: false}}, metadata, nil, hardware.Snapshot{RAMAvailableBytes: 16 * gib}, 4096, nil, true, true)
	if incomplete.Artifacts[0].Fit != FitUnknown || incomplete.Artifacts[0].Runnable { t.Fatalf("incomplete=%+v", incomplete.Artifacts[0]) }
}

func TestAnalyzeDiscoverEdgeOrderingAndBounds(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	metadata := Metadata{Architecture: "llama", ContextLength: 32768, BlockCount: 32, Embedding: 4096, HeadCount: 32, KVHeadCount: 8}
	snapshot := hardware.Snapshot{RAMAvailableBytes: 32 * gib, RAMTotalBytes: 48 * gib}
	inputs := []ArtifactInput{
		{ID: "larger-q4", Quantization: "Q4_K_M", WeightsBytes: 5 * gib, Complete: true},
		{ID: "smaller-q4", Quantization: "Q4_K_S", WeightsBytes: 4 * gib, Complete: true},
		{ID: "unknown", Quantization: "experimental", WeightsBytes: 1 * gib, Complete: true},
		{ID: "zero-size", Quantization: "Q5_K_M", WeightsBytes: 0, Complete: true},
	}
	result := AnalyzeDiscover(inputs, metadata, nil, snapshot, 4096, nil, true, true)
	if len(result.Artifacts) != 4 || result.Artifacts[0].ArtifactID != "smaller-q4" || !result.Artifacts[0].Recommended {
		t.Fatalf("ordered=%+v", result.Artifacts)
	}
	var zero, unknown DiscoverArtifact
	for _, artifact := range result.Artifacts {
		switch artifact.ArtifactID {
		case "zero-size": zero = artifact
		case "unknown": unknown = artifact
		}
	}
	if zero.Fit != FitUnknown || zero.Runnable || !strings.Contains(zero.Reason, "size is unavailable") {
		t.Fatalf("zero=%+v", zero)
	}
	if !unknown.Runnable || unknown.Quantization.Known || unknown.Recommended {
		t.Fatalf("unknown=%+v", unknown)
	}

	overContext := AnalyzeDiscover([]ArtifactInput{{ID: "q4", Quantization: "Q4_K_M", WeightsBytes: 4 * gib, Complete: true}}, metadata, nil, snapshot, 65536, nil, true, true)
	if overContext.Artifacts[0].Fit != FitNo || overContext.Artifacts[0].Runnable || !strings.Contains(overContext.Artifacts[0].Reason, "larger than the context capability") {
		t.Fatalf("over-context=%+v", overContext.Artifacts[0])
	}

	if fit, label := discoverFit(true, Offload{Mode: "future-mode"}); fit != FitUnknown || label != "Fit unknown" {
		t.Fatalf("unexpected future-mode fit=%q label=%q", fit, label)
	}
	if hardwareTelemetryAvailable(hardware.Snapshot{}, contextUnavailableError{}) {
		t.Fatal("explicit unavailable telemetry must stay unavailable")
	}
	if !hardwareTelemetryAvailable(hardware.Snapshot{}, nil) {
		t.Fatal("a successful zero-valued snapshot remains a valid telemetry response")
	}
	if (contextUnavailableError{}).Error() != "hardware telemetry unavailable" {
		t.Fatal("unexpected contextUnavailableError text")
	}
}

func TestAnalyzeDiscoverAssumeIdleIgnoresOccupancy(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	metadata := Metadata{Architecture: "llama", ContextLength: 32768, BlockCount: 32, Embedding: 4096, HeadCount: 32, KVHeadCount: 8}
	input := []ArtifactInput{{ID: "q4", Quantization: "Q4_K_M", WeightsBytes: 4 * gib, Complete: true}}
	occupied := hardware.Snapshot{
		RAMAvailableBytes: 2 * gib,
		RAMTotalBytes:     32 * gib,
		GPUs:              []hardware.GPU{{ID: "CUDA0", FreeBytes: 1 * gib, TotalBytes: 8 * gib}},
	}

	idle := AnalyzeDiscover(input, metadata, nil, occupied, 4096, nil, true, true)
	if !idle.AssumeIdle || idle.Artifacts[0].Fit != FitGPU || !idle.Artifacts[0].Runnable {
		t.Fatalf("assume-idle=%+v", idle.Artifacts[0])
	}

	current := AnalyzeDiscover(input, metadata, nil, occupied, 4096, nil, true, false)
	if current.AssumeIdle || current.Artifacts[0].Fit == FitGPU {
		t.Fatalf("current-occupancy should not claim full GPU fit: %+v", current.Artifacts[0])
	}
}

func TestAssumeIdleSnapshotDoesNotMutateCaller(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 4 * gib,
		RAMTotalBytes:     32 * gib,
		GPUs:              []hardware.GPU{{ID: "CUDA0", FreeBytes: 1 * gib, TotalBytes: 8 * gib}},
	}
	idle := assumeIdleSnapshot(snapshot)
	if idle.RAMAvailableBytes != 32*gib || idle.GPUs[0].FreeBytes != 8*gib {
		t.Fatalf("idle=%+v", idle)
	}
	if snapshot.RAMAvailableBytes != 4*gib || snapshot.GPUs[0].FreeBytes != 1*gib {
		t.Fatalf("caller mutated=%+v", snapshot)
	}
	idle.GPUs[0].FreeBytes = 0
	if snapshot.GPUs[0].FreeBytes != 1*gib {
		t.Fatalf("shared slice mutated caller=%+v", snapshot)
	}
}
