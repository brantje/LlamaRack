package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAvailableGGUFsExcludesMultimodalProjectorsByMetadata(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)

	nested := filepath.Join(dir, "vision-model")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeClassifiedGGUF(t, nested, "model-Q5_K_M.gguf", "llama", 0, true)
	writeClassifiedGGUF(t, nested, "generic-vision-helper.gguf", "clip", 0, false)
	writeClassifiedGGUF(t, nested, "asda-projector.gguf", "llama", 0, true)

	files, err := s.AvailableGGUFs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("available files=%+v", files)
	}
	if !isProjectorGGUF(filepath.Join(nested, "generic-vision-helper.gguf")) {
		t.Fatal("clip architecture must be recognized as projector regardless of filename")
	}
	if isProjectorGGUF(filepath.Join(nested, "asda-projector.gguf")) {
		t.Fatal("projector-looking filename must not override non-clip GGUF metadata")
	}
}
