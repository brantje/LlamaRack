package recommendations

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

func TestAnalyzeFullPartialCPUAndTotalFit(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	model := models.Model{ID: "m1", TotalBytes: 4 * gib, Quantization: "Q4_K_M"}
	metaPath := writeGGUF(t, map[string]any{
		"general.architecture": "llama",
		"llama.context_length": int64(8192),
		"llama.block_count": int64(32),
		"llama.embedding_length": int64(4096),
		"llama.attention.head_count": int64(32),
		"llama.attention.head_count_kv": int64(8),
	})

	full := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 16*gib, RAMTotalBytes: 32*gib, GPUs: []hardware.GPU{{ID:"CUDA0", FreeBytes:8*gib, TotalBytes:8*gib}}}, 4096, nil)
	if !full.CurrentFit || full.Offload.Mode != "full" || full.Offload.GPULayers != 32 || full.Confidence != "high" || full.Memory.KVCacheBytes <= 0 || full.ContextAssumed {
		t.Fatalf("unexpected full recommendation: %+v", full)
	}
	if !strings.Contains(full.Quantization.Summary, "Balanced") { t.Fatalf("missing Q4 explanation: %+v", full.Quantization) }

	partial := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 16*gib, RAMTotalBytes: 32*gib, GPUs: []hardware.GPU{{ID:"CUDA0", FreeBytes:3*gib, TotalBytes:8*gib}}}, 4096, nil)
	if partial.CurrentFit || partial.Offload.Mode != "partial" || partial.Offload.GPULayers <= 0 || !partial.TotalHardwareFit {
		t.Fatalf("unexpected partial recommendation: %+v", partial)
	}

	cpu := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 8*gib, RAMTotalBytes: 16*gib}, 0, errors.New("gpu probe failed"))
	if !cpu.CurrentFit || !cpu.CPUFit || cpu.Offload.Mode != "cpu" || cpu.ContextLength != 8192 || cpu.HardwareWarning == "" {
		t.Fatalf("unexpected cpu recommendation: %+v", cpu)
	}
}

func TestAnalyzeFallsBackWhenMetadataInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil { t.Fatal(err) }
	r := Analyze(models.Model{ID:"m", TotalBytes:1024, Quantization:"Q8_0"}, path, hardware.Snapshot{}, 0, nil)
	if r.Confidence != "low" || !r.ContextAssumed || r.ContextLength != defaultContext || r.MetadataWarning == "" || !strings.Contains(r.Quantization.Summary, "Large") {
		t.Fatalf("fallback=%+v", r)
	}
}

func TestQuantizationFamilies(t *testing.T) {
	for _, tc := range []struct{ q, word string }{{"Q2_K","compact"},{"Q5_K_M","fidelity"},{"Q6_K","fidelity"},{"F16","precision"},{"mystery","unknown"}} {
		info := ExplainQuantization(tc.q)
		if !strings.Contains(strings.ToLower(info.Summary), tc.word) { t.Fatalf("%s => %+v", tc.q, info) }
	}
}

func TestReadMetadataRejectsBadInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.gguf")
	if err := os.WriteFile(path, []byte("GGUF\x01\x00\x00\x00"), 0o644); err != nil { t.Fatal(err) }
	if _, err := ReadMetadata(path); err == nil { t.Fatal("expected unsupported/truncated GGUF to fail") }
	if _, err := ReadMetadata(filepath.Join(t.TempDir(), "missing.gguf")); err == nil { t.Fatal("expected missing file") }
}

func writeGGUF(t *testing.T, metadata map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model-Q4_K_M.gguf")
	f, err := os.Create(path); if err != nil { t.Fatal(err) }
	defer f.Close()
	if _, err := f.Write([]byte("GGUF")); err != nil { t.Fatal(err) }
	mustWrite(t, f, uint32(3)); mustWrite(t, f, uint64(0)); mustWrite(t, f, uint64(len(metadata)))
	keys := []string{"general.architecture","llama.context_length","llama.block_count","llama.embedding_length","llama.attention.head_count","llama.attention.head_count_kv"}
	for _, key := range keys {
		value, ok := metadata[key]; if !ok { continue }
		writeString(t, f, key)
		switch v := value.(type) {
		case string:
			mustWrite(t, f, uint32(8)); writeString(t, f, v)
		case int64:
			mustWrite(t, f, uint32(11)); mustWrite(t, f, v)
		default: t.Fatalf("unsupported fixture metadata %T", value)
		}
	}
	return path
}

func writeString(t *testing.T, f *os.File, value string) { t.Helper(); mustWrite(t, f, uint64(len(value))); if _, err := f.Write([]byte(value)); err != nil { t.Fatal(err) } }
func mustWrite(t *testing.T, f *os.File, value any) { t.Helper(); if err := binary.Write(f, binary.LittleEndian, value); err != nil { t.Fatal(err) } }
