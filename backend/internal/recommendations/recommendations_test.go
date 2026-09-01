package recommendations

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/models"
)

func TestAnalyzeFullPartialHybridCPUAndTotalFit(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	model := models.Model{ID: "m1", TotalBytes: 4 * gib, Quantization: "Q4_K_M", ContextLength: 262144}
	metaPath := writeMetadataGGUF(t, "qwen2", map[string]int64{
		"qwen2.context_length": 262144, "qwen2.block_count": 32, "qwen2.embedding_length": 4096,
		"qwen2.attention.head_count": 32, "qwen2.attention.head_count_kv": 8,
	})
	full := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 16 * gib, RAMTotalBytes: 32 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib, TotalBytes: 8 * gib}}}, 4096, nil)
	if !full.CurrentFit || full.Offload.Mode != "full" || !full.Offload.KVOnGPU || full.Offload.GPULayers != 32 || full.Confidence != "high" || full.Memory.KVCacheBytes <= 0 || full.ContextAssumed || full.ContextCapability != 262144 {
		t.Fatalf("full=%+v", full)
	}
	if !strings.Contains(full.Quantization.Summary, "Balanced") {
		t.Fatalf("quant=%+v", full.Quantization)
	}

	partial := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 16 * gib, RAMTotalBytes: 32 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 3 * gib, TotalBytes: 8 * gib}}}, 4096, nil)
	if !partial.CurrentFit || partial.Offload.Mode != "partial" || !partial.Offload.KVOnGPU || partial.Offload.GPULayers <= 0 || !partial.TotalHardwareFit {
		t.Fatalf("partial=%+v", partial)
	}

	hybrid := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 16 * gib, RAMTotalBytes: 32 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib, TotalBytes: 8 * gib}}}, 65536, nil)
	if !hybrid.CurrentFit || hybrid.Offload.Mode != "hybrid" || hybrid.Offload.KVOnGPU || hybrid.Offload.GPULayers != 32 {
		t.Fatalf("hybrid=%+v", hybrid)
	}

	cpu := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 8 * gib, RAMTotalBytes: 16 * gib}, 0, errors.New("gpu probe failed"))
	if !cpu.CurrentFit || !cpu.CPUFit || cpu.Offload.Mode != "cpu" || cpu.ContextLength != defaultContext || !cpu.ContextAssumed || cpu.HardwareWarning == "" {
		t.Fatalf("cpu=%+v", cpu)
	}
}

func TestRecommendationDecisionBranches(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	m := Metadata{BlockCount: 40, Embedding: 4096, HeadCount: 32, KVHeadCount: 8}
	memory := estimateMemory(8*gib, 4096, m)
	fit, multi := recommendOffload(hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 6 * gib, TotalBytes: 6 * gib}, {ID: "CUDA1", FreeBytes: 6 * gib, TotalBytes: 6 * gib}}}, memory, m)
	if !fit || multi.Mode != "multi_gpu" || len(multi.Devices) != 2 || multi.TensorSplit == "" {
		t.Fatalf("multi=%+v fit=%v", multi, fit)
	}
	fit, partial := recommendOffload(hardware.Snapshot{RAMAvailableBytes: 16 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 3 * gib, TotalBytes: 8 * gib}}}, estimateMemory(4*gib, 4096, Metadata{}), Metadata{})
	if !fit || partial.Mode != "partial" || partial.GPULayers != 0 {
		t.Fatalf("partial=%+v", partial)
	}
	fit, tiny := recommendOffload(hardware.Snapshot{RAMAvailableBytes: 16 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 128 * mib, TotalBytes: 8 * gib}}}, memory, m)
	if !fit || tiny.Mode != "cpu" {
		t.Fatalf("tiny=%+v", tiny)
	}
	fit, noRAM := recommendOffload(hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 128 * mib, TotalBytes: 8 * gib}}}, memory, m)
	if fit || noRAM.Mode != "cpu" {
		t.Fatalf("noRAM=%+v", noRAM)
	}
	if fit, empty := recommendOffload(hardware.Snapshot{}, memory, m); fit || empty.Mode != "" {
		t.Fatalf("empty=%+v", empty)
	}
}

func TestHelpersAndQuantizationBranches(t *testing.T) {
	if got, assumed := chooseContext(16384); got != 16384 || assumed {
		t.Fatalf("context=%d %v", got, assumed)
	}
	if got, assumed := chooseContext(0); got != defaultContext || !assumed {
		t.Fatalf("default=%d %v", got, assumed)
	}
	if got := estimateMemory(-1, 4096, Metadata{}); got.WeightsBytes != 0 || got.RuntimeOverheadBytes != 256*mib {
		t.Fatalf("memory=%+v", got)
	}
	if estimateKV(0, Metadata{}) != 0 {
		t.Fatal("zero kv")
	}
	if estimateKV(4096, Metadata{BlockCount: 2, Embedding: 16, HeadCount: 4}) <= 0 {
		t.Fatal("fallback kv heads")
	}
	if estimateKV(4096, Metadata{BlockCount: 2, Embedding: 2, HeadCount: 4}) != 0 {
		t.Fatal("invalid head dimension")
	}
	if confidence(Metadata{Architecture: "llama"}, errors.New("partial")) != "medium" || confidence(Metadata{}, errors.New("bad")) != "low" {
		t.Fatal("confidence")
	}
	if recommendedLayers(0, .5) != 0 || recommendedLayers(32, 0) != 0 || recommendedLayers(32, .001) != 1 || recommendedLayers(32, 2) != 32 {
		t.Fatal("layer bounds")
	}
	if fitsRAM(defaultRAMReserve, 0) || !fitsRAM(2*defaultRAMReserve, defaultRAMReserve) {
		t.Fatal("RAM boundary")
	}
	for _, tc := range []struct{ q, word string }{{"Q2_K", "compact"}, {"Q3_K_M", "compact"}, {"Q5_K_M", "fidelity"}, {"Q6_K", "fidelity"}, {"Q8_0", "large"}, {"F16", "precision"}, {"BF16", "precision"}, {"F32", "precision"}, {"mystery", "unknown"}} {
		info := ExplainQuantization(tc.q)
		if !strings.Contains(strings.ToLower(info.Summary), tc.word) {
			t.Fatalf("%s=%+v", tc.q, info)
		}
	}
}

func TestAnalyzeAndReadMetadataGracefulFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.gguf")
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Analyze(models.Model{ID: "m", TotalBytes: 1024, Quantization: "Q8_0"}, path, hardware.Snapshot{}, 0, nil)
	if r.Confidence != "low" || r.MetadataWarning == "" || !r.ContextAssumed {
		t.Fatalf("fallback=%+v", r)
	}
	if _, err := ReadMetadata(filepath.Join(t.TempDir(), "missing.gguf")); err == nil {
		t.Fatal("missing should fail")
	}

	good := writeMetadataGGUF(t, "gemma3", map[string]int64{
		"gemma3.context_length": 131072, "gemma3.block_count": 24, "gemma3.embedding_length": 2048,
		"gemma3.attention.head_count": 8, "other.context_length": 1,
	})
	m, err := ReadMetadata(good)
	if err != nil || m.Architecture != "gemma3" || m.ContextLength != 131072 || m.BlockCount != 24 {
		t.Fatalf("metadata=%+v err=%v", m, err)
	}
}

func writeMetadataGGUF(t *testing.T, architecture string, ints map[string]int64) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	mustWrite(t, &b, uint32(3))
	mustWrite(t, &b, uint64(0))
	mustWrite(t, &b, uint64(1+len(ints)))
	writeString(t, &b, "general.architecture")
	mustWrite(t, &b, uint32(8))
	writeString(t, &b, architecture)
	for key, value := range ints {
		writeString(t, &b, key)
		mustWrite(t, &b, uint32(11))
		mustWrite(t, &b, value)
	}
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	mustWrite(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func mustWrite(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
