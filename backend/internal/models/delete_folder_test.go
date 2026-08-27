package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteFilesAndModelRemovesNestedModelDirectory(t *testing.T) {
	ctx := context.Background()
	s, root := testModelService(t)
	modelDir := filepath.Join(root, "owner", "repo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := writeGGUF(t, modelDir, "model-Q4_K_M.gguf")
	if err := os.WriteFile(filepath.Join(modelDir, "README.txt"), []byte("download metadata"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideHelper := writeGGUF(t, root, "shared-location-mmproj.gguf")
	model, err := s.Create(ctx, CreateModelInput{
		Name:     "Nested",
		GGUFPath: main,
		Options:  map[string]string{"mmproj": outsideHelper},
	})
	if err != nil {
		t.Fatal(err)
	}

	plan, err := s.PrepareFileDeletion(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.directory == nil || plan.directory.relativePath != "owner/repo" {
		t.Fatalf("unexpected model directory plan: %+v", plan.directory)
	}
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(modelDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("model directory still exists: %v", err)
	}
	if _, err := os.Stat(outsideHelper); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("associated helper outside model directory still exists: %v", err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("configured models root was removed: info=%v err=%v", info, err)
	}
}

func TestDeleteFilesAndModelNeverRemovesModelsRoot(t *testing.T) {
	ctx := context.Background()
	s, root := testModelService(t)
	main := writeGGUF(t, root, "root-model.gguf")
	neighbor := writeGGUF(t, root, "neighbor.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Root model", GGUFPath: main})
	if err != nil {
		t.Fatal(err)
	}

	plan, err := s.PrepareFileDeletion(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.directory != nil {
		t.Fatalf("models root must not become a recursive deletion target: %+v", plan.directory)
	}
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(neighbor); err != nil {
		t.Fatalf("neighbor in models root was removed: %v", err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("models root was removed: info=%v err=%v", info, err)
	}
}

func TestPrepareFileDeletionRefusesDirectoryContainingAnotherModel(t *testing.T) {
	ctx := context.Background()
	s, root := testModelService(t)
	modelDir := filepath.Join(root, "shared-folder")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	firstPath := writeGGUF(t, modelDir, "first.gguf")
	secondPath := writeGGUF(t, modelDir, "second.gguf")
	first, err := s.Create(ctx, CreateModelInput{Name: "First", GGUFPath: firstPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateModelInput{Name: "Second", GGUFPath: secondPath}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PrepareFileDeletion(ctx, first.ID); !errors.Is(err, ErrArtifactShared) {
		t.Fatalf("expected shared model directory refusal, got %v", err)
	}
	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("first model file was touched: %v", err)
	}
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("second model file was touched: %v", err)
	}
}

func TestModelDirectoryDeleteFailureKeepsRegistrationAndFiles(t *testing.T) {
	ctx := context.Background()
	s, root := testModelService(t)
	modelDir := filepath.Join(root, "io-failure")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := writeGGUF(t, modelDir, "model.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Directory failure", GGUFPath: main})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PrepareFileDeletion(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}

	originalRemoveDirectory := removeModelDirectory
	removeModelDirectory = func(string) error { return errors.New("permission denied") }
	t.Cleanup(func() { removeModelDirectory = originalRemoveDirectory })
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err == nil {
		t.Fatal("expected model directory deletion failure")
	}
	if _, err := s.GetByID(ctx, model.ID); err != nil {
		t.Fatalf("directory failure removed Model registration: %v", err)
	}
	if _, err := os.Stat(main); err != nil {
		t.Fatalf("directory failure removed model file before directory removal: %v", err)
	}
}

func TestPrepareFileDeletionRejectsSymlinkedModelDirectory(t *testing.T) {
	ctx := context.Background()
	s, root := testModelService(t)
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := writeGGUF(t, realDir, "model.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Symlinked folder", GGUFPath: main})
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE models SET gguf_path=? WHERE id=?`, filepath.ToSlash(filepath.Join("alias", "model.gguf")), model.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PrepareFileDeletion(ctx, model.ID); !errors.Is(err, ErrUnsafeArtifactPath) {
		t.Fatalf("expected symlinked model directory rejection, got %v", err)
	}
	if _, err := os.Stat(main); err != nil {
		t.Fatalf("symlink rejection touched model file: %v", err)
	}
}
