package models

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func BenchmarkAvailableGGUFs20(b *testing.B) {
	s, dir := benchmarkModelService(b)
	for i := 0; i < 20; i++ {
		benchmarkWriteGGUF(b, dir, fmt.Sprintf("model-%02d-Q4_K_M.gguf", i), "llama", 131072, false)
	}
	ctx := context.Background()
	// Populate the persistent metadata index before timing so this benchmark
	// measures the steady-state warm discovery path explicitly. The original
	// benchmark effectively did this after its first iteration because b.N is
	// large, but an explicit warm-up makes the benchmark semantics unambiguous.
	if _, err := s.AvailableGGUFs(ctx); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.AvailableGGUFs(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAvailableGGUFsSplit16(b *testing.B) {
	s, dir := benchmarkModelService(b)
	for i := 1; i <= 16; i++ {
		benchmarkWriteGGUF(b, dir, fmt.Sprintf("split-model-Q4_K_M-%05d-of-00016.gguf", i), "llama", 131072, false)
	}
	ctx := context.Background()
	if _, err := s.AvailableGGUFs(ctx); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.AvailableGGUFs(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkModelService(tb testing.TB) (*Service, string) {
	tb.Helper()
	root := tb.TempDir()
	modelsDir := filepath.Join(root, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		tb.Fatal(err)
	}
	db, err := database.Open(context.Background(), filepath.Join(root, "manager.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	return New(db, modelsDir), modelsDir
}

func benchmarkWriteGGUF(tb testing.TB, dir, name, architecture string, contextLength uint32, mtp bool) string {
	tb.Helper()
	var buf bytes.Buffer
	buf.WriteString("GGUF")
	benchmarkWrite(tb, &buf, uint32(3))
	benchmarkWrite(tb, &buf, uint64(1)) // tensor count
	metadataCount := uint64(2)
	if mtp {
		metadataCount++
	}
	benchmarkWrite(tb, &buf, metadataCount)
	benchmarkString(tb, &buf, "general.architecture")
	benchmarkWrite(tb, &buf, uint32(8))
	benchmarkString(tb, &buf, architecture)
	benchmarkString(tb, &buf, architecture+".context_length")
	benchmarkWrite(tb, &buf, uint32(4))
	benchmarkWrite(tb, &buf, contextLength)
	if mtp {
		benchmarkString(tb, &buf, architecture+".nextn_predict_layers")
		benchmarkWrite(tb, &buf, uint32(4))
		benchmarkWrite(tb, &buf, uint32(1))
	}
	benchmarkString(tb, &buf, "blk.0.attn_norm.weight")
	benchmarkWrite(tb, &buf, uint32(1))
	benchmarkWrite(tb, &buf, uint64(1))
	benchmarkWrite(tb, &buf, uint32(0))
	benchmarkWrite(tb, &buf, uint64(0))

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		tb.Fatal(err)
	}
	return path
}

func benchmarkString(tb testing.TB, buf *bytes.Buffer, value string) {
	tb.Helper()
	benchmarkWrite(tb, buf, uint64(len(value)))
	_, _ = buf.WriteString(value)
}

func benchmarkWrite(tb testing.TB, buf *bytes.Buffer, value any) {
	tb.Helper()
	if err := binary.Write(buf, binary.LittleEndian, value); err != nil {
		tb.Fatal(err)
	}
}
