package models

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
)

// BenchmarkDetailsPathInspectAndSummary mirrors the pending model-details
// handler: full InspectGGUF followed by GGUFSummary to attach features.
func BenchmarkDetailsPathInspectAndSummary(b *testing.B) {
	s, path := setupDetailsPathService(b, 16384, 256)
	ctx := context.Background()
	if _, err := s.InspectGGUF(path); err != nil {
		b.Fatal(err)
	}
	if _, err := s.GGUFSummary(ctx, path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inspection, err := s.InspectGGUF(path)
		if err != nil {
			b.Fatal(err)
		}
		summary, err := s.GGUFSummary(ctx, path)
		if err != nil {
			b.Fatal(err)
		}
		if inspection.MetadataCount == 0 || summary.Features.Architecture == "" {
			b.Fatalf("inspection=%+v summary=%+v", inspection.MetadataCount, summary.Features)
		}
	}
}

func BenchmarkDetailsPathInspectOnly(b *testing.B) {
	s, path := setupDetailsPathService(b, 16384, 256)
	if _, err := s.InspectGGUF(path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inspection, err := s.InspectGGUF(path)
		if err != nil {
			b.Fatal(err)
		}
		if inspection.MetadataCount == 0 {
			b.Fatal("empty inspection")
		}
	}
}

func BenchmarkDetailsPathSummaryCached(b *testing.B) {
	s, path := setupDetailsPathService(b, 16384, 256)
	ctx := context.Background()
	if _, err := s.GGUFSummary(ctx, path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		summary, err := s.GGUFSummary(ctx, path)
		if err != nil {
			b.Fatal(err)
		}
		if summary.Features.Architecture == "" {
			b.Fatal("empty summary")
		}
	}
}

func BenchmarkDetailsPathFeaturesFromInspection(b *testing.B) {
	s, path := setupDetailsPathService(b, 16384, 256)
	inspection, err := s.InspectGGUF(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		features := ggufmeta.FeaturesFromInspection(inspection)
		if features.Architecture == "" {
			b.Fatal("empty features")
		}
	}
}

func BenchmarkInspectGGUFArtifactWithCompanions(b *testing.B) {
	s, dir := benchmarkModelService(b)
	main := writeClassifiedGGUF(b, dir, "model-Q5_K_M.gguf", "qwen2", 0, true)
	_ = writeClassifiedGGUF(b, dir, "mmproj-F16.gguf", "clip", 0, false)
	_ = writeClassifiedGGUF(b, dir, "draft-Q4_0.gguf", "qwen35", 1, false)
	ctx := context.Background()
	if _, err := s.InspectGGUFArtifact(ctx, main); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inspection, err := s.InspectGGUFArtifact(ctx, main)
		if err != nil {
			b.Fatal(err)
		}
		if inspection.Architecture == "" || len(inspection.Dependencies) != 2 {
			b.Fatalf("inspection=%+v", inspection)
		}
	}
}

func BenchmarkDetailsJSONEncode(b *testing.B) {
	s, path := setupDetailsPathService(b, 16384, 256)
	inspection, err := s.InspectGGUF(path)
	if err != nil {
		b.Fatal(err)
	}
	page, total := ggufmeta.Filter(inspection.Metadata, "", 0, 100)
	payload := map[string]any{
		"gguf_version":   inspection.Version,
		"tensor_count":   inspection.TensorCount,
		"metadata_count": inspection.MetadataCount,
		"metadata_total": total,
		"metadata":       page,
		"architecture":   inspection.Derived.Architecture,
		"features":       ggufmeta.FeaturesFromInspection(inspection),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encoded, err := json.Marshal(payload)
		if err != nil {
			b.Fatal(err)
		}
		if len(encoded) < 100 {
			b.Fatal("short payload")
		}
	}
}

func setupDetailsPathService(b *testing.B, tokens, tensors int) (*Service, string) {
	b.Helper()
	s, dir := benchmarkModelService(b)
	path := writeRealisticGGUF(b, dir, "details-Q4_K_M.gguf", tokens, tensors)
	return s, path
}

func writeRealisticGGUF(tb testing.TB, dir, name string, tokens, tensors int) string {
	tb.Helper()
	if tokens < 1 {
		tokens = 1
	}
	if tensors < 1 {
		tensors = 1
	}
	var buf bytes.Buffer
	buf.WriteString("GGUF")
	detailsBinary(tb, &buf, uint32(3))
	detailsBinary(tb, &buf, uint64(tensors))
	detailsBinary(tb, &buf, uint64(5))
	detailsMetaString(tb, &buf, "general.architecture", "qwen2")
	detailsMetaU32(tb, &buf, "qwen2.context_length", 32768)
	detailsString(tb, &buf, "tokenizer.ggml.tokens")
	detailsBinary(tb, &buf, uint32(9))
	detailsBinary(tb, &buf, uint32(8))
	detailsBinary(tb, &buf, uint64(tokens))
	for i := 0; i < tokens; i++ {
		detailsString(tb, &buf, fmt.Sprintf("tok%05d", i))
	}
	detailsString(tb, &buf, "tokenizer.ggml.scores")
	detailsBinary(tb, &buf, uint32(9))
	detailsBinary(tb, &buf, uint32(6))
	detailsBinary(tb, &buf, uint64(tokens))
	for i := 0; i < tokens; i++ {
		detailsBinary(tb, &buf, float32(i))
	}
	detailsString(tb, &buf, "tokenizer.ggml.token_type")
	detailsBinary(tb, &buf, uint32(9))
	detailsBinary(tb, &buf, uint32(4))
	detailsBinary(tb, &buf, uint64(tokens))
	for i := 0; i < tokens; i++ {
		detailsBinary(tb, &buf, uint32(1))
	}
	for i := 0; i < tensors; i++ {
		detailsString(tb, &buf, fmt.Sprintf("blk.%d.attn_norm.weight", i))
		detailsBinary(tb, &buf, uint32(1))
		detailsBinary(tb, &buf, uint64(1))
		detailsBinary(tb, &buf, uint32(0))
		detailsBinary(tb, &buf, uint64(0))
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		tb.Fatal(err)
	}
	return path
}

func detailsMetaString(tb testing.TB, buf *bytes.Buffer, key, value string) {
	tb.Helper()
	detailsString(tb, buf, key)
	detailsBinary(tb, buf, uint32(8))
	detailsString(tb, buf, value)
}

func detailsMetaU32(tb testing.TB, buf *bytes.Buffer, key string, value uint32) {
	tb.Helper()
	detailsString(tb, buf, key)
	detailsBinary(tb, buf, uint32(4))
	detailsBinary(tb, buf, value)
}

func detailsString(tb testing.TB, buf *bytes.Buffer, value string) {
	tb.Helper()
	detailsBinary(tb, buf, uint64(len(value)))
	_, _ = buf.WriteString(value)
}

func detailsBinary(tb testing.TB, buf *bytes.Buffer, value any) {
	tb.Helper()
	if err := binary.Write(buf, binary.LittleEndian, value); err != nil {
		tb.Fatal(err)
	}
}
