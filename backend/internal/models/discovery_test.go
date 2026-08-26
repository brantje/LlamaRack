package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIsProjectorGGUF(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"mmproj-model-f16.gguf", true},
		{"model-mmproj-Q5_K_M.gguf", true},
		{"foo-MMOPROJ-Q8_0.GGUF", true},
		{"asda-projector.gguf", true},
		{"vision/projector/model.gguf", true},
		{"vision/mmproj/model.gguf", true},
		{"qwen-vision-Q4_K_M.gguf", false},
		{"projection-model.gguf", false},
		{"project-model.gguf", false},
		{"model-proj.gguf", false},
	} {
		if got := isProjectorGGUF(tc.path); got != tc.want {
			t.Errorf("isProjectorGGUF(%q)=%v want=%v", tc.path, got, tc.want)
		}
	}
}

func TestAvailableGGUFsExcludesProjectorPaths(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	writeGGUF(t, dir, "real-model-Q4_K_M.gguf")
	writeGGUF(t, dir, "asda-projector.gguf")
	writeGGUF(t, dir, "model-mmproj-Q8_0.gguf")

	projectorDir := filepath.Join(dir, "vision-projectors")
	if err := os.MkdirAll(projectorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGGUF(t, projectorDir, "generic.gguf")

	files, err := s.AvailableGGUFs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "real-model-Q4_K_M.gguf" {
		t.Fatalf("available files=%+v", files)
	}
}
