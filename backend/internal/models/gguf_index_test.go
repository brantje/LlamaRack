package models

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/ggufmeta"
)

func TestGGUFSummaryUsesFingerprintCacheAndInvalidates(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := benchmarkWriteGGUF(t, dir, "cached-Q4_K_M.gguf", "llama", 131072, false)

	first, err := s.GGUFSummary(ctx, path)
	if err != nil || first.Derived.Architecture != "llama" || first.Derived.ContextLength != 131072 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mtime := info.ModTime()
	corrupt := make([]byte, len(original))
	copy(corrupt, []byte("not-a-gguf"))
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().UnixNano() != mtime.UnixNano() {
		t.Skip("filesystem did not preserve nanosecond mtime for fingerprint test")
	}

	cached, err := s.GGUFSummary(ctx, path)
	if err != nil || cached.Derived.Architecture != "llama" {
		t.Fatalf("cached=%+v err=%v", cached, err)
	}

	changed := mtime.Add(2 * time.Second)
	if err := os.Chtimes(path, changed, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GGUFSummary(ctx, path); err == nil {
		t.Fatal("changed fingerprint should force reinspection of corrupt file")
	}
	var warning string
	if err := s.db.QueryRowContext(ctx, `SELECT inspect_error FROM gguf_index WHERE path='cached-Q4_K_M.gguf'`).Scan(&warning); err != nil || warning == "" {
		t.Fatalf("warning=%q err=%v", warning, err)
	}
}

func TestAvailableGGUFsIndexesOnlyFirstSplitShard(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	for i := 1; i <= 16; i++ {
		benchmarkWriteGGUF(t, dir, fmt.Sprintf("split-Q4_K_M-%05d-of-00016.gguf", i), "llama", 32768, false)
	}
	files, err := s.AvailableGGUFs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].ContextLength != 32768 || files[0].Architecture != "llama" {
		t.Fatalf("files=%+v", files)
	}
	var indexed int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gguf_index`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 1 {
		t.Fatalf("indexed shards=%d want=1", indexed)
	}
}

func TestAvailableGGUFsReusesIndexAndRemovesMissingRows(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	one := benchmarkWriteGGUF(t, dir, "one.gguf", "llama", 4096, false)
	benchmarkWriteGGUF(t, dir, "two.gguf", "qwen2", 8192, false)
	files, err := s.AvailableGGUFs(ctx)
	if err != nil || len(files) != 2 {
		t.Fatalf("first scan=%+v err=%v", files, err)
	}
	files, err = s.AvailableGGUFs(ctx)
	if err != nil || len(files) != 2 {
		t.Fatalf("warm scan=%+v err=%v", files, err)
	}
	if err := os.Remove(one); err != nil {
		t.Fatal(err)
	}
	files, err = s.AvailableGGUFs(ctx)
	if err != nil || len(files) != 1 || filepath.Base(files[0].Path) != "two.gguf" {
		t.Fatalf("after remove=%+v err=%v", files, err)
	}
	var indexed int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gguf_index`).Scan(&indexed); err != nil || indexed != 1 {
		t.Fatalf("indexed=%d err=%v", indexed, err)
	}
}

func TestGGUFIndexErrorPaths(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	if _, err := s.GGUFSummary(ctx, filepath.Join(dir, "missing.gguf")); err == nil {
		t.Fatal("missing GGUF should fail normal path validation")
	}
	if err := s.storeGGUFIndex(ctx, "overflow.gguf", ggufIndexEntry{
		Summary: ggufmeta.Summary{TensorCount: ^uint64(0)},
	}); err == nil {
		t.Fatal("oversized tensor count should not be written to SQLite")
	}
	if err := s.storeGGUFIndex(ctx, "overflow.gguf", ggufIndexEntry{
		Summary: ggufmeta.Summary{MetadataCount: ^uint64(0)},
	}); err == nil {
		t.Fatal("oversized metadata count should not be written to SQLite")
	}

	path := benchmarkWriteGGUF(t, dir, "closed.gguf", "llama", 4096, false)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.loadGGUFIndex(ctx); err == nil {
		t.Fatal("loading index from closed database should fail")
	}
	if _, err := s.GGUFSummary(ctx, path); err == nil {
		t.Fatal("summary lookup with closed database should fail")
	}
	if _, _, err := s.cachedGGUFSummary(ctx, dir, "closed.gguf", info.Size(), info.ModTime().UnixNano(), map[string]ggufIndexEntry{}); err == nil {
		t.Fatal("cache miss must surface index write failure")
	}
	if err := s.removeMissingGGUFIndex(ctx, map[string]ggufIndexEntry{"stale.gguf": {}}, map[string]bool{}); err == nil {
		t.Fatal("stale-row deletion from closed database should fail")
	}
}

func TestGGUFIndexUintBounds(t *testing.T) {
	if got, err := ggufIndexUint(7); err != nil || got != 7 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	if _, err := ggufIndexUint(^uint64(0)); err == nil {
		t.Fatal("overflow should fail")
	}
}
