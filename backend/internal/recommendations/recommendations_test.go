package recommendations

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

func TestAnalyzeFullPartialHybridCPUAndTotalFit(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	model := models.Model{ID: "m1", TotalBytes: 4 * gib, Quantization: "Q4_K_M", ContextLength: 262144}
	metaPath := writeGGUF(t, map[string]any{
		"general.architecture": "llama",
		"llama.context_length": int64(262144),
		"llama.block_count": int64(32),
		"llama.embedding_length": int64(4096),
		"llama.attention.head_count": int64(32),
		"llama.attention.head_count_kv": int64(8),
	})

	full := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 16 * gib, RAMTotalBytes: 32 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib, TotalBytes: 8 * gib}}}, 4096, nil)
	if !full.CurrentFit || full.Offload.Mode != "full" || !full.Offload.KVOnGPU || full.Offload.GPULayers != 32 || full.Confidence != "high" || full.Memory.KVCacheBytes <= 0 || full.ContextAssumed || full.ContextCapability != 262144 {
		t.Fatalf("unexpected full recommendation: %+v", full)
	}
	if !strings.Contains(full.Quantization.Summary, "Balanced") {
		t.Fatalf("missing Q4 explanation: %+v", full.Quantization)
	}

	partial := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 16 * gib, RAMTotalBytes: 32 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 3 * gib, TotalBytes: 8 * gib}}}, 4096, nil)
	if !partial.CurrentFit || partial.Offload.Mode != "partial" || !partial.Offload.KVOnGPU || partial.Offload.GPULayers <= 0 || !partial.TotalHardwareFit {
		t.Fatalf("unexpected partial recommendation: %+v", partial)
	}

	hybrid := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 16 * gib, RAMTotalBytes: 32 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib, TotalBytes: 8 * gib}}}, 65536, nil)
	if !hybrid.CurrentFit || hybrid.Offload.Mode != "hybrid" || hybrid.Offload.KVOnGPU || hybrid.Offload.GPULayers != 32 || hybrid.Offload.Devices[0] != "CUDA0" {
		t.Fatalf("unexpected hybrid recommendation: %+v", hybrid)
	}

	cpu := Analyze(model, metaPath, hardware.Snapshot{RAMAvailableBytes: 8 * gib, RAMTotalBytes: 16 * gib}, 0, errors.New("gpu probe failed"))
	if !cpu.CurrentFit || !cpu.CPUFit || cpu.Offload.Mode != "cpu" || cpu.ContextLength != defaultContext || !cpu.ContextAssumed || cpu.ContextCapability != 262144 || cpu.HardwareWarning == "" {
		t.Fatalf("unexpected cpu recommendation: %+v", cpu)
	}
}

func TestRecommendationDecisionBranches(t *testing.T) {
	gib := int64(1024 * 1024 * 1024)
	m := Metadata{BlockCount: 40, Embedding: 4096, HeadCount: 32, KVHeadCount: 8}
	memory := estimateMemory(8*gib, 4096, m)

	fit, multi := recommendOffload(hardware.Snapshot{GPUs: []hardware.GPU{
		{ID: "CUDA0", FreeBytes: 6 * gib, TotalBytes: 6 * gib},
		{ID: "CUDA1", FreeBytes: 6 * gib, TotalBytes: 6 * gib},
	}}, memory, m)
	if !fit || multi.Mode != "multi_gpu" || !multi.KVOnGPU || len(multi.Devices) != 2 || multi.TensorSplit == "" {
		t.Fatalf("multi=%+v fit=%v", multi, fit)
	}

	fit, partialUnknownLayers := recommendOffload(hardware.Snapshot{RAMAvailableBytes: 16 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 3 * gib, TotalBytes: 8 * gib}}}, estimateMemory(4*gib, 4096, Metadata{}), Metadata{})
	if !fit || partialUnknownLayers.Mode != "partial" || !partialUnknownLayers.KVOnGPU || partialUnknownLayers.GPULayers != 0 {
		t.Fatalf("partial without block count=%+v fit=%v", partialUnknownLayers, fit)
	}

	largeContext := estimateMemory(4*gib, 65536, Metadata{BlockCount: 32, Embedding: 4096, HeadCount: 32, KVHeadCount: 8})
	fit, hybrid := recommendOffload(hardware.Snapshot{RAMAvailableBytes: 16 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 * gib, TotalBytes: 8 * gib}}}, largeContext, Metadata{BlockCount: 32, Embedding: 4096, HeadCount: 32, KVHeadCount: 8})
	if !fit || hybrid.Mode != "hybrid" || hybrid.KVOnGPU || hybrid.GPULayers != 32 {
		t.Fatalf("hybrid=%+v fit=%v", hybrid, fit)
	}

	fit, tinyGPUCPU := recommendOffload(hardware.Snapshot{RAMAvailableBytes: 16 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 128 * mib, TotalBytes: 8 * gib}}}, memory, m)
	if !fit || tinyGPUCPU.Mode != "cpu" {
		t.Fatalf("tiny gpu cpu fallback=%+v fit=%v", tinyGPUCPU, fit)
	}

	fit, noVRAM := recommendOffload(hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 128 * mib, TotalBytes: 8 * gib}}}, memory, m)
	if fit || noVRAM.Mode != "cpu" {
		t.Fatalf("no vram=%+v fit=%v", noVRAM, fit)
	}

	lowHeadroom := hardware.Snapshot{RAMAvailableBytes: 16 * gib, GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 600 * mib, TotalBytes: 8 * gib}}}
	fit, cpuAfterGPUCheck := recommendOffload(lowHeadroom, estimateMemory(1*gib, 4096, Metadata{}), Metadata{})
	if !fit || cpuAfterGPUCheck.Mode != "cpu" {
		t.Fatalf("cpu after gpu check=%+v fit=%v", cpuAfterGPUCheck, fit)
	}

	if fit, empty := recommendOffload(hardware.Snapshot{}, memory, m); fit || empty.Mode != "" {
		t.Fatalf("empty gpu recommendation=%+v fit=%v", empty, fit)
	}
}

func TestContextMemoryKVAndConfidenceBranches(t *testing.T) {
	if got, assumed := chooseContext(16384); got != 16384 || assumed {
		t.Fatalf("explicit context=%d assumed=%v", got, assumed)
	}
	if got, assumed := chooseContext(0); got != defaultContext || !assumed {
		t.Fatalf("default context=%d assumed=%v", got, assumed)
	}
	if got := estimateMemory(-1, 4096, Metadata{}); got.WeightsBytes != 0 || got.RuntimeOverheadBytes != 256*mib {
		t.Fatalf("negative weights=%+v", got)
	}
	if got := estimateKV(0, Metadata{}); got != 0 {
		t.Fatalf("zero context kv=%d", got)
	}
	if got := estimateKV(4096, Metadata{BlockCount: 2, Embedding: 16, HeadCount: 4}); got <= 0 {
		t.Fatalf("fallback kv heads=%d", got)
	}
	if got := estimateKV(4096, Metadata{BlockCount: 2, Embedding: 2, HeadCount: 4}); got != 0 {
		t.Fatalf("invalid head dimension kv=%d", got)
	}
	if got := confidence(Metadata{Architecture: "llama"}, errors.New("partial")); got != "medium" {
		t.Fatalf("medium confidence=%q", got)
	}
	if got := recommendedLayers(0, 0.5); got != 0 {
		t.Fatalf("zero block layers=%d", got)
	}
	if got := recommendedLayers(32, 0); got != 0 {
		t.Fatalf("zero fraction layers=%d", got)
	}
	if got := recommendedLayers(32, 0.001); got != 1 {
		t.Fatalf("minimum layers=%d", got)
	}
	if got := recommendedLayers(32, 2); got != 32 {
		t.Fatalf("capped layers=%d", got)
	}
	if fitsRAM(defaultRAMReserve, 0) || !fitsRAM(2*defaultRAMReserve, defaultRAMReserve) {
		t.Fatal("unexpected RAM fit boundary")
	}
}

func TestAnalyzeFallsBackWhenMetadataInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Analyze(models.Model{ID: "m", TotalBytes: 1024, Quantization: "Q8_0"}, path, hardware.Snapshot{}, 0, nil)
	if r.Confidence != "low" || !r.ContextAssumed || r.ContextLength != defaultContext || r.MetadataWarning == "" || !strings.Contains(r.Quantization.Summary, "Large") {
		t.Fatalf("fallback=%+v", r)
	}
}

func TestQuantizationFamilies(t *testing.T) {
	for _, tc := range []struct{ q, word string }{{"Q2_K", "compact"}, {"Q3_K_M", "compact"}, {"Q5_K_M", "fidelity"}, {"Q6_K", "fidelity"}, {"F16", "precision"}, {"BF16", "precision"}, {"F32", "precision"}, {"mystery", "unknown"}} {
		info := ExplainQuantization(tc.q)
		if !strings.Contains(strings.ToLower(info.Summary), tc.word) {
			t.Fatalf("%s => %+v", tc.q, info)
		}
	}
}

func TestReadMetadataRejectsBadInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.gguf")
	if err := os.WriteFile(path, []byte("GGUF\x01\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMetadata(path); err == nil {
		t.Fatal("expected unsupported/truncated GGUF to fail")
	}
	if _, err := ReadMetadata(filepath.Join(t.TempDir(), "missing.gguf")); err == nil {
		t.Fatal("expected missing file")
	}

	truncated := filepath.Join(t.TempDir(), "truncated.gguf")
	if err := os.WriteFile(truncated, []byte("GGUF\x03\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMetadata(truncated); err == nil {
		t.Fatal("expected truncated header")
	}

	unreasonable := filepath.Join(t.TempDir(), "count.gguf")
	f, err := os.Create(unreasonable)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("GGUF"))
	mustWrite(t, f, uint32(3))
	mustWrite(t, f, uint64(0))
	mustWrite(t, f, uint64(1_000_001))
	_ = f.Close()
	if _, err := ReadMetadata(unreasonable); err == nil || !strings.Contains(err.Error(), "metadata count") {
		t.Fatalf("unreasonable metadata error=%v", err)
	}
}

func TestReadValueCoversGGUFScalarStringArrayAndErrors(t *testing.T) {
	tests := []struct {
		name   string
		typeID uint32
		data   []byte
		want   any
	}{
		{"u8", 0, []byte{7}, int64(7)},
		{"i8", 1, []byte{0xff}, int64(-1)},
		{"u16", 2, little(uint16(7)), int64(7)},
		{"i16", 3, little(int16(-2)), int64(-2)},
		{"u32", 4, little(uint32(9)), int64(9)},
		{"i32", 5, little(int32(-3)), int64(-3)},
		{"f32-skipped", 6, little(uint32(0)), nil},
		{"bool", 7, []byte{1}, int64(1)},
		{"u64", 10, little(uint64(11)), int64(11)},
		{"i64", 11, little(int64(-12)), int64(-12)},
		{"f64-skipped", 12, little(uint64(0)), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readValue(bytes.NewReader(tc.data), tc.typeID, true)
			if err != nil || got != tc.want {
				t.Fatalf("got=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}

	var text bytes.Buffer
	mustBinaryWrite(t, &text, uint64(5))
	text.WriteString("llama")
	if got, err := readValue(bytes.NewReader(text.Bytes()), 8, true); err != nil || got != "llama" {
		t.Fatalf("string got=%v err=%v", got, err)
	}
	if got, err := readValue(bytes.NewReader(text.Bytes()), 8, false); err != nil || got != nil {
		t.Fatalf("discarded string got=%v err=%v", got, err)
	}

	var array bytes.Buffer
	mustBinaryWrite(t, &array, uint32(4))
	mustBinaryWrite(t, &array, uint64(2))
	mustBinaryWrite(t, &array, uint32(1))
	mustBinaryWrite(t, &array, uint32(2))
	if got, err := readValue(bytes.NewReader(array.Bytes()), 9, true); err != nil || got != nil {
		t.Fatalf("array got=%v err=%v", got, err)
	}

	var huge bytes.Buffer
	mustBinaryWrite(t, &huge, uint32(4))
	mustBinaryWrite(t, &huge, uint64(10_000_001))
	if _, err := readValue(bytes.NewReader(huge.Bytes()), 9, true); err == nil || !strings.Contains(err.Error(), "array") {
		t.Fatalf("huge array err=%v", err)
	}
	if _, err := readValue(bytes.NewReader(nil), 99, true); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported type err=%v", err)
	}
	if _, err := readValue(bytes.NewReader(nil), 4, true); err == nil {
		t.Fatal("expected truncated scalar")
	}
}

func TestReadStringBoundsAndRelevantMetadata(t *testing.T) {
	var huge bytes.Buffer
	mustBinaryWrite(t, &huge, uint64(16*mib+1))
	if _, err := readString(bytes.NewReader(huge.Bytes())); err == nil || !strings.Contains(err.Error(), "string") {
		t.Fatalf("huge string err=%v", err)
	}
	var truncated bytes.Buffer
	mustBinaryWrite(t, &truncated, uint64(4))
	truncated.WriteString("ab")
	if _, err := readString(bytes.NewReader(truncated.Bytes())); err == nil {
		t.Fatal("expected truncated string")
	}
	for _, key := range []string{"general.architecture", "llama.context_length", "llama.attention.key_length", "llama.attention.value_length"} {
		if !relevantKey(key) {
			t.Fatalf("expected relevant key %q", key)
		}
	}
	if relevantKey("general.name") {
		t.Fatal("general.name should not be retained")
	}
}

func writeGGUF(t *testing.T, metadata map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model-Q4_K_M.gguf")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write([]byte("GGUF")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, f, uint32(3))
	mustWrite(t, f, uint64(0))
	mustWrite(t, f, uint64(len(metadata)))
	keys := []string{"general.architecture", "llama.context_length", "llama.block_count", "llama.embedding_length", "llama.attention.head_count", "llama.attention.head_count_kv"}
	for _, key := range keys {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		writeString(t, f, key)
		switch v := value.(type) {
		case string:
			mustWrite(t, f, uint32(8))
			writeString(t, f, v)
		case int64:
			mustWrite(t, f, uint32(11))
			mustWrite(t, f, v)
		default:
			t.Fatalf("unsupported fixture metadata %T", value)
		}
	}
	return path
}

func little(value any) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, value)
	return buf.Bytes()
}

func mustBinaryWrite(t *testing.T, buf *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(buf, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}

func writeString(t *testing.T, f *os.File, value string) {
	t.Helper()
	mustWrite(t, f, uint64(len(value)))
	if _, err := f.Write([]byte(value)); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, f *os.File, value any) {
	t.Helper()
	if err := binary.Write(f, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
