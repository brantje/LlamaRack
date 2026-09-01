package ggufmeta

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSummaryDerivedAndSkipsUnneededMetadata(t *testing.T) {
	path := writeGGUF(t, 3, 7, []kv{
		{"general.architecture", 8, func(b *bytes.Buffer) { writeString(b, "qwen2") }},
		{"qwen2.context_length", 4, func(b *bytes.Buffer) { write(b, uint32(32768)) }},
		{"qwen2.block_count", 10, func(b *bytes.Buffer) { write(b, uint64(40)) }},
		{"qwen2.embedding_length", 11, func(b *bytes.Buffer) { write(b, int64(4096)) }},
		{"qwen2.attention.head_count", 2, func(b *bytes.Buffer) { write(b, uint16(32)) }},
		{"qwen2.attention.head_count_kv", 0, func(b *bytes.Buffer) { b.WriteByte(8) }},
		{"qwen2.attention.key_length", 3, func(b *bytes.Buffer) { write(b, int16(128)) }},
		{"qwen2.attention.value_length", 5, func(b *bytes.Buffer) { write(b, int32(128)) }},
		{"ignored.fixed", 4, func(b *bytes.Buffer) { write(b, uint32(99)) }},
		{"ignored.string", 8, func(b *bytes.Buffer) { writeString(b, "ignored") }},
		{"ignored.array", 9, func(b *bytes.Buffer) {
			write(b, uint32(4))
			write(b, uint64(20))
			for i := 0; i < 20; i++ {
				write(b, uint32(i))
			}
		}},
		{"ignored.strings", 9, func(b *bytes.Buffer) {
			write(b, uint32(8))
			write(b, uint64(2))
			writeString(b, "a")
			writeString(b, "b")
		}},
	})
	summary, err := ReadSummary(path)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Version != 3 || summary.TensorCount != 7 || summary.MetadataCount != 12 {
		t.Fatalf("header=%+v", summary)
	}
	d := summary.Derived
	if d.Architecture != "qwen2" || d.ContextLength != 32768 || d.BlockCount != 40 || d.Embedding != 4096 || d.HeadCount != 32 || d.KVHeadCount != 8 || d.KeyLength != 128 || d.ValueLength != 128 {
		t.Fatalf("derived=%+v", d)
	}
	if summary.Features.Architecture != "qwen2" || summary.Features.Projector || summary.Features.HasMTP || summary.Features.MTPOnly {
		t.Fatalf("features=%+v", summary.Features)
	}
}

func TestReadSummaryClassifiesProjectorNativeMTPAndMTPOnly(t *testing.T) {
	projector := writeFeatureGGUF(t, "clip", 0, "v.blk.0.attn.weight")
	summary, err := ReadSummary(projector)
	if err != nil || !summary.Features.Projector || summary.Features.HasMTP {
		t.Fatalf("projector=%+v err=%v", summary.Features, err)
	}

	native := writeFeatureGGUF(t, "qwen35", 1, "blk.0.attn_norm.weight", "blk.40.nextn.eh_proj.weight")
	summary, err = ReadSummary(native)
	if err != nil || !summary.Features.HasMTP || summary.Features.MTPOnly || summary.Features.NextNPredictLayers != 1 {
		t.Fatalf("native=%+v err=%v", summary.Features, err)
	}

	draft := writeFeatureGGUF(t, "qwen35", 1, "token_embd.weight", "blk.40.nextn.eh_proj.weight")
	summary, err = ReadSummary(draft)
	if err != nil || !summary.Features.HasMTP || !summary.Features.MTPOnly {
		t.Fatalf("draft=%+v err=%v", summary.Features, err)
	}
}

func TestSummaryFeatureEdgeCases(t *testing.T) {
	if got := summaryFeatures(Derived{}, nil); got.Architecture != "" || got.HasMTP {
		t.Fatalf("empty=%+v", got)
	}
	derived := Derived{Architecture: "qwen"}
	if got := summaryFeatures(derived, map[string]string{"qwen.nextn_predict_layers": "bad"}); got.HasMTP {
		t.Fatalf("invalid=%+v", got)
	}
	if got := summaryFeatures(derived, map[string]string{"qwen.nextn_predict_layers": "9223372036854775808"}); got.HasMTP {
		t.Fatalf("too-large=%+v", got)
	}
	if got := summaryFeatures(Derived{Architecture: " clip "}, nil); !got.Projector {
		t.Fatalf("projector=%+v", got)
	}
}

func TestReadSummaryRejectsMalformedInput(t *testing.T) {
	if _, err := ReadSummary(filepath.Join(t.TempDir(), "missing.gguf")); err == nil {
		t.Fatal("missing should fail")
	}
	bad := filepath.Join(t.TempDir(), "bad.gguf")
	if err := os.WriteFile(bad, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSummary(bad); err == nil || !strings.Contains(err.Error(), "magic") {
		t.Fatalf("magic=%v", err)
	}
	if _, err := ReadSummary(writeHeader(t, 1, 0, 0)); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version=%v", err)
	}
	if _, err := ReadSummary(writeHeader(t, 3, 0, maxMetadataCount+1)); err == nil || !strings.Contains(err.Error(), "metadata count") {
		t.Fatalf("metadata=%v", err)
	}
	nested := writeGGUF(t, 3, 0, []kv{{"ignored", 9, func(b *bytes.Buffer) { write(b, uint32(9)); write(b, uint64(1)) }}})
	if _, err := ReadSummary(nested); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("nested=%v", err)
	}
	huge := writeGGUF(t, 3, 0, []kv{{"ignored", 9, func(b *bytes.Buffer) { write(b, uint32(4)); write(b, maxArrayCount+1) }}})
	if _, err := ReadSummary(huge); err == nil || !strings.Contains(err.Error(), "array") {
		t.Fatalf("array=%v", err)
	}
	unsupported := writeGGUF(t, 3, 0, []kv{{"ignored", 99, func(*bytes.Buffer) {}}})
	if _, err := ReadSummary(unsupported); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("type=%v", err)
	}

	truncatedCounts := filepath.Join(t.TempDir(), "counts.gguf")
	if err := os.WriteFile(truncatedCounts, []byte("GGUF\x03\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSummary(truncatedCounts); err == nil {
		t.Fatal("truncated header counts should fail")
	}
	var malformedValue bytes.Buffer
	malformedValue.WriteString("GGUF")
	write(&malformedValue, uint32(3))
	write(&malformedValue, uint64(0))
	write(&malformedValue, uint64(1))
	writeString(&malformedValue, "general.architecture")
	write(&malformedValue, uint32(8))
	write(&malformedValue, uint64(5))
	malformedValue.WriteString("x")
	path := filepath.Join(t.TempDir(), "value.gguf")
	if err := os.WriteFile(path, malformedValue.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSummary(path); err == nil {
		t.Fatal("truncated selected value should fail")
	}
}

func TestSummarySkipHelpersAndTensorBounds(t *testing.T) {
	fixed := bytes.NewReader(make([]byte, 4))
	if err := skipSummaryValue(fixed, 4); err != nil {
		t.Fatal(err)
	}
	if pos, _ := fixed.Seek(0, io.SeekCurrent); pos != 4 {
		t.Fatalf("fixed pos=%d", pos)
	}

	var text bytes.Buffer
	writeString(&text, "hello")
	if err := skipSummaryValue(bytes.NewReader(text.Bytes()), 8); err != nil {
		t.Fatal(err)
	}
	var hugeString bytes.Buffer
	write(&hugeString, maxStringBytes+1)
	if err := skipSummaryValue(bytes.NewReader(hugeString.Bytes()), 8); err == nil {
		t.Fatal("huge string should fail")
	}
	if err := skipSummaryValue(bytes.NewReader(nil), 99); err == nil {
		t.Fatal("unsupported value should fail")
	}
	if err := skipSummaryValue(bytes.NewReader(nil), 9); err == nil {
		t.Fatal("array missing element type should fail")
	}
	var noCount bytes.Buffer
	write(&noCount, uint32(4))
	if err := skipSummaryValue(bytes.NewReader(noCount.Bytes()), 9); err == nil {
		t.Fatal("array missing count should fail")
	}
	var unsupportedArray bytes.Buffer
	write(&unsupportedArray, uint32(99))
	write(&unsupportedArray, uint64(0))
	if err := skipSummaryValue(bytes.NewReader(unsupportedArray.Bytes()), 9); err == nil {
		t.Fatal("unsupported array element should fail")
	}
	var truncatedStrings bytes.Buffer
	write(&truncatedStrings, uint32(8))
	write(&truncatedStrings, uint64(1))
	write(&truncatedStrings, uint64(3))
	truncatedStrings.WriteByte('x')
	if err := skipSummaryValue(bytes.NewReader(truncatedStrings.Bytes()), 9); err != nil {
		t.Fatalf("seek over a truncated final string should itself succeed: %v", err)
	}

	var badDims bytes.Buffer
	writeString(&badDims, "tensor")
	write(&badDims, maxTensorDimensions+1)
	if _, err := tensorPrefixPresentCurrent(bytes.NewReader(badDims.Bytes()), 1, "blk.0."); err == nil {
		t.Fatal("bad dimensions should fail")
	}
	if _, err := tensorPrefixPresentCurrent(bytes.NewReader(nil), 1, "blk.0."); err == nil {
		t.Fatal("missing tensor name should fail")
	}
	var truncatedTensor bytes.Buffer
	writeString(&truncatedTensor, "tensor")
	write(&truncatedTensor, uint32(1))
	if _, err := tensorPrefixPresentCurrent(bytes.NewReader(truncatedTensor.Bytes()), 1, "blk.0."); err == nil {
		t.Fatal("missing tensor dimension should fail")
	}
	var missingType bytes.Buffer
	writeString(&missingType, "tensor")
	write(&missingType, uint32(0))
	if _, err := tensorPrefixPresentCurrent(bytes.NewReader(missingType.Bytes()), 1, "blk.0."); err == nil {
		t.Fatal("missing tensor type should fail")
	}
	var missingOffset bytes.Buffer
	writeString(&missingOffset, "tensor")
	write(&missingOffset, uint32(0))
	write(&missingOffset, uint32(0))
	if _, err := tensorPrefixPresentCurrent(bytes.NewReader(missingOffset.Bytes()), 1, "blk.0."); err == nil {
		t.Fatal("missing tensor offset should fail")
	}
}
