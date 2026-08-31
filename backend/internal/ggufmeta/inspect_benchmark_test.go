package ggufmeta

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkInspectSmall(b *testing.B) {
	path := writeBenchmarkGGUF(b, benchmarkGGUFSpec{tokens: 8, tensors: 4, extraScalars: 4})
	benchmarkInspect(b, path)
}

func BenchmarkInspectRealistic(b *testing.B) {
	path := writeBenchmarkGGUF(b, benchmarkGGUFSpec{tokens: 16384, tensors: 256, extraScalars: 32})
	benchmarkInspect(b, path)
}

func BenchmarkReadSummarySmall(b *testing.B) {
	path := writeBenchmarkGGUF(b, benchmarkGGUFSpec{tokens: 8, tensors: 4, extraScalars: 4})
	benchmarkReadSummary(b, path)
}

func BenchmarkReadSummaryRealistic(b *testing.B) {
	path := writeBenchmarkGGUF(b, benchmarkGGUFSpec{tokens: 16384, tensors: 256, extraScalars: 32})
	benchmarkReadSummary(b, path)
}

func BenchmarkFilterEmptyQuery(b *testing.B) {
	path := writeBenchmarkGGUF(b, benchmarkGGUFSpec{tokens: 64, tensors: 8, extraScalars: 200})
	inspection, err := Inspect(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, total := Filter(inspection.Metadata, "", 0, 100)
		if total < 100 || len(page) != 100 {
			b.Fatalf("page=%d total=%d", len(page), total)
		}
	}
}

func BenchmarkFilterQuery(b *testing.B) {
	path := writeBenchmarkGGUF(b, benchmarkGGUFSpec{tokens: 64, tensors: 8, extraScalars: 200})
	inspection, err := Inspect(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		page, total := Filter(inspection.Metadata, "architecture", 0, 10)
		if total == 0 || len(page) == 0 {
			b.Fatalf("page=%d total=%d", len(page), total)
		}
	}
}

func BenchmarkFeaturesFromInspection(b *testing.B) {
	path := writeBenchmarkGGUF(b, benchmarkGGUFSpec{tokens: 64, tensors: 8, extraScalars: 32, nextN: 1})
	inspection, err := Inspect(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		features := FeaturesFromInspection(inspection)
		if !features.HasMTP {
			b.Fatal("expected MTP features")
		}
	}
}

func BenchmarkDetectFeaturesNativeMTP(b *testing.B) {
	path := writeBenchmarkGGUF(b, benchmarkGGUFSpec{tokens: 64, tensors: 256, extraScalars: 8, nextN: 1, trunk: true})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		features, err := DetectFeatures(path)
		if err != nil {
			b.Fatal(err)
		}
		if !features.HasMTP || features.MTPOnly {
			b.Fatalf("features=%+v", features)
		}
	}
}

func benchmarkInspect(b *testing.B, path string) {
	b.Helper()
	if _, err := Inspect(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inspection, err := Inspect(path)
		if err != nil {
			b.Fatal(err)
		}
		if inspection.MetadataCount == 0 {
			b.Fatal("empty inspection")
		}
	}
}

func benchmarkReadSummary(b *testing.B, path string) {
	b.Helper()
	if _, err := ReadSummary(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		summary, err := ReadSummary(path)
		if err != nil {
			b.Fatal(err)
		}
		if summary.MetadataCount == 0 {
			b.Fatal("empty summary")
		}
	}
}

type benchmarkGGUFSpec struct {
	tokens       int
	tensors      int
	extraScalars int
	nextN        uint32
	trunk        bool
}

func writeBenchmarkGGUF(tb testing.TB, spec benchmarkGGUFSpec) string {
	tb.Helper()
	if spec.tokens < 1 {
		spec.tokens = 1
	}
	if spec.tensors < 1 {
		spec.tensors = 1
	}

	var buf bytes.Buffer
	buf.WriteString("GGUF")
	benchmarkBinary(&buf, uint32(3))
	benchmarkBinary(&buf, uint64(spec.tensors))

	metadataCount := uint64(5 + spec.extraScalars) // architecture, context, tokens, scores, token_type
	if spec.nextN > 0 {
		metadataCount++
	}
	benchmarkBinary(&buf, metadataCount)

	benchmarkMetaString(&buf, "general.architecture", "qwen35")
	benchmarkMetaU32(&buf, "qwen35.context_length", 32768)
	if spec.nextN > 0 {
		benchmarkMetaU32(&buf, "qwen35.nextn_predict_layers", spec.nextN)
	}
	for i := 0; i < spec.extraScalars; i++ {
		benchmarkMetaU32(&buf, fmt.Sprintf("extra.key_%03d", i), uint32(i))
	}

	benchmarkMetaStringKey(&buf, "tokenizer.ggml.tokens")
	benchmarkBinary(&buf, uint32(9))
	benchmarkBinary(&buf, uint32(8))
	benchmarkBinary(&buf, uint64(spec.tokens))
	for i := 0; i < spec.tokens; i++ {
		benchmarkString(&buf, fmt.Sprintf("tok%05d", i))
	}

	benchmarkMetaStringKey(&buf, "tokenizer.ggml.scores")
	benchmarkBinary(&buf, uint32(9))
	benchmarkBinary(&buf, uint32(6))
	benchmarkBinary(&buf, uint64(spec.tokens))
	for i := 0; i < spec.tokens; i++ {
		benchmarkBinary(&buf, float32(i))
	}

	benchmarkMetaStringKey(&buf, "tokenizer.ggml.token_type")
	benchmarkBinary(&buf, uint32(9))
	benchmarkBinary(&buf, uint32(4))
	benchmarkBinary(&buf, uint64(spec.tokens))
	for i := 0; i < spec.tokens; i++ {
		benchmarkBinary(&buf, uint32(1))
	}

	for i := 0; i < spec.tensors; i++ {
		name := fmt.Sprintf("blk.%d.attn_norm.weight", i)
		if spec.nextN > 0 && !spec.trunk && i == 0 {
			name = "token_embd.weight"
		} else if spec.trunk && i == 0 {
			name = "blk.0.attn_norm.weight"
		}
		benchmarkString(&buf, name)
		benchmarkBinary(&buf, uint32(1))
		benchmarkBinary(&buf, uint64(1))
		benchmarkBinary(&buf, uint32(0))
		benchmarkBinary(&buf, uint64(0))
	}

	path := filepath.Join(tb.TempDir(), "bench.gguf")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		tb.Fatal(err)
	}
	return path
}

func benchmarkMetaString(buf *bytes.Buffer, key, value string) {
	benchmarkMetaStringKey(buf, key)
	benchmarkBinary(buf, uint32(8))
	benchmarkString(buf, value)
}

func benchmarkMetaU32(buf *bytes.Buffer, key string, value uint32) {
	benchmarkMetaStringKey(buf, key)
	benchmarkBinary(buf, uint32(4))
	benchmarkBinary(buf, value)
}

func benchmarkMetaStringKey(buf *bytes.Buffer, key string) {
	benchmarkString(buf, key)
}

func benchmarkString(buf *bytes.Buffer, value string) {
	benchmarkBinary(buf, uint64(len(value)))
	buf.WriteString(value)
}

func benchmarkBinary(buf *bytes.Buffer, value any) {
	if err := binary.Write(buf, binary.LittleEndian, value); err != nil {
		panic(err)
	}
}
