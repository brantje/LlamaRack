package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAvailableGGUFsGroupsCompleteSplits(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	for _, file := range []struct {
		name string
		data string
	}{
		{"nested/demo-Q4_K_M-00001-of-00002.gguf", "abc"},
		{"nested/demo-Q4_K_M-00002-of-00002.gguf", "defg"},
		{"broken-Q8_0-00001-of-00002.gguf", "x"},
		{"single-Q5_K_M.gguf", "12345"},
	} {
		path := filepath.Join(dir, filepath.FromSlash(file.name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(file.data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := s.AvailableGGUFs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("available = %+v", files)
	}
	var split, single GGUFFile
	for _, file := range files {
		if file.Path == "nested/demo-Q4_K_M-00001-of-00002.gguf" {
			split = file
		}
		if file.Path == "single-Q5_K_M.gguf" {
			single = file
		}
	}
	if split.TotalBytes != 7 || split.Quantization != "Q4_K_M" {
		t.Fatalf("split = %+v", split)
	}
	if single.TotalBytes != 5 || single.Quantization != "Q5_K_M" {
		t.Fatalf("single = %+v", single)
	}
}

func TestAvailableGGUFsHidesRegisteredSplitSet(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	writeGGUF(t, dir, "demo-Q4_K_M-00001-of-00002.gguf")
	writeGGUF(t, dir, "demo-Q4_K_M-00002-of-00002.gguf")
	if _, err := s.Create(ctx, CreateModelInput{Name: "Demo", GGUFPath: "demo-Q4_K_M-00001-of-00002.gguf"}); err != nil {
		t.Fatal(err)
	}
	files, err := s.AvailableGGUFs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("registered split leaked shards: %+v", files)
	}
}
