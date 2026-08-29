package models

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestAvailableGGUFsExposeModificationTime(t *testing.T) {
	s, dir := testModelService(t)
	path := writeClassifiedGGUF(t, dir, "model-Q4_K_M.gguf", "qwen2", 0, true)
	modified := time.Date(2026, time.August, 29, 18, 30, 0, 123000000, time.UTC)
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}

	items, err := s.AvailableGGUFs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("available = %+v", items)
	}
	got, err := time.Parse(time.RFC3339Nano, items[0].ModifiedAt)
	if err != nil {
		t.Fatalf("modified_at=%q: %v", items[0].ModifiedAt, err)
	}
	if !got.Equal(modified) {
		t.Fatalf("modified_at=%v want %v", got, modified)
	}
}

func TestAvailableSplitGGUFUsesNewestShardModificationTime(t *testing.T) {
	s, dir := testModelService(t)
	first := writeClassifiedGGUF(t, dir, "model-Q4_K_M-00001-of-00002.gguf", "qwen2", 0, true)
	second := writeClassifiedGGUF(t, dir, "model-Q4_K_M-00002-of-00002.gguf", "qwen2", 0, true)
	older := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	newer := older.Add(5 * time.Hour)
	if err := os.Chtimes(first, older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(second, newer, newer); err != nil {
		t.Fatal(err)
	}

	items, err := s.AvailableGGUFs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("available = %+v", items)
	}
	got, err := time.Parse(time.RFC3339Nano, items[0].ModifiedAt)
	if err != nil {
		t.Fatalf("modified_at=%q: %v", items[0].ModifiedAt, err)
	}
	if !got.Equal(newer) {
		t.Fatalf("modified_at=%v want newest shard %v", got, newer)
	}
}
