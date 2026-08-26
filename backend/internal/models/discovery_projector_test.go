package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAvailableGGUFsExcludesMultimodalProjectors(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)

	nested := filepath.Join(dir, "vision-model")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGGUF(t, nested, "model-Q5_K_M.gguf")
	writeGGUF(t, nested, "mmproj-BF16.gguf")
	writeGGUF(t, nested, "mmproj-model-f16.gguf")
	writeGGUF(t, nested, "mmoproj-Q8_0.gguf")

	files, err := s.AvailableGGUFs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("available files=%+v", files)
	}
	if files[0].Path != "vision-model/model-Q5_K_M.gguf" {
		t.Fatalf("unexpected available file: %+v", files[0])
	}

	for _, name := range []string{"mmproj-BF16.gguf", "MMPROJ-F16.GGUF", "mmoproj-Q8_0.gguf"} {
		if !isProjectorGGUF(name) {
			t.Fatalf("expected %q to be recognized as projector", name)
		}
	}
	if isProjectorGGUF("my-mmproj-model.gguf") {
		t.Fatal("only projector-prefixed filenames should be filtered")
	}
}
