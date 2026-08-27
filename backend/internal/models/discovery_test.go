package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIsProjectorGGUFUsesMetadata(t *testing.T) {
	dir := t.TempDir()
	projector := writeClassifiedGGUF(t, dir, "plain.gguf", "clip", 0, false)
	main := writeClassifiedGGUF(t, dir, "mmproj-looking.gguf", "qwen2", 0, true)
	if !isProjectorGGUF(projector) {
		t.Fatal("clip GGUF should be projector")
	}
	if isProjectorGGUF(main) {
		t.Fatal("filename must not classify a normal model as projector")
	}
}

func TestAvailableGGUFsExcludesProjectorMetadata(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	writeClassifiedGGUF(t, dir, "real-model-Q4_K_M.gguf", "llama", 0, true)
	writeClassifiedGGUF(t, dir, "not-named-mmproj.gguf", "clip", 0, false)

	projectorDir := filepath.Join(dir, "vision-projectors")
	if err := os.MkdirAll(projectorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClassifiedGGUF(t, projectorDir, "ordinary-name.gguf", "clip", 0, false)

	files, err := s.AvailableGGUFs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "real-model-Q4_K_M.gguf" {
		t.Fatalf("available files=%+v", files)
	}
}
