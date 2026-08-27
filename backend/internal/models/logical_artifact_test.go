package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRefreshLogicalSizeSumsCompleteSplitGGUF(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	first := filepath.Join(dir, "demo-Q4_K_M-00001-of-00002.gguf")
	second := filepath.Join(dir, "demo-Q4_K_M-00002-of-00002.gguf")
	if err := os.WriteFile(first, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("defgh"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := s.Create(ctx, CreateModelInput{Name: "Split", GGUFPath: first})
	if err != nil {
		t.Fatal(err)
	}
	if model.TotalBytes != 3 {
		t.Fatalf("pre-refresh total=%d", model.TotalBytes)
	}
	refreshed, err := s.RefreshLogicalSize(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.TotalBytes != 8 {
		t.Fatalf("logical total=%d", refreshed.TotalBytes)
	}
	if size, err := s.LogicalGGUFSize(first); err != nil || size != 8 {
		t.Fatalf("logical size=%d err=%v", size, err)
	}
}

func TestLogicalGGUFSizeRejectsIncompleteSplit(t *testing.T) {
	s, dir := testModelService(t)
	first := filepath.Join(dir, "broken-Q8_0-00001-of-00002.gguf")
	if err := os.WriteFile(first, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LogicalGGUFSize(first); err == nil {
		t.Fatal("incomplete split should fail logical-size resolution")
	}
}
