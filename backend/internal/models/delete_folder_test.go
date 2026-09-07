package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteFilesAndModelPreservesUnownedNestedFiles(t *testing.T) {
	ctx := context.Background()
	s, root := testModelService(t)
	modelDir := filepath.Join(root, "owner", "repo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := writeGGUF(t, modelDir, "model-Q4_K_M.gguf")
	readme := filepath.Join(modelDir, "README.txt")
	if err := os.WriteFile(readme, []byte("download metadata"), 0o644); err != nil {
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
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(main); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("primary nested GGUF still exists: %v", err)
	}
	if _, err := os.Stat(readme); err != nil {
		t.Fatalf("unregistered nested file was removed: %v", err)
	}
	if _, err := os.Stat(outsideHelper); err != nil {
		t.Fatalf("unowned companion outside model directory was removed: %v", err)
	}
	if _, err := os.Stat(modelDir); err != nil {
		t.Fatalf("non-empty model directory was removed: %v", err)
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

func TestDeleteFilesAndModelAllowsSiblingModelInSameFolder(t *testing.T) {
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

	plan, err := s.PrepareFileDeletion(ctx, first.ID)
	if err != nil {
		t.Fatalf("sibling in the same folder should not block deletion: %v", err)
	}
	if err := s.DeleteFilesAndModel(ctx, first.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first model file still exists: %v", err)
	}
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("second model file was removed: %v", err)
	}
	if _, err := os.Stat(modelDir); err != nil {
		t.Fatalf("shared folder was removed while it still held another model: %v", err)
	}
}

func TestEmptyDirectoryPruneFailureKeepsRegistration(t *testing.T) {
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

	originalRemoveDirectory := removeEmptyDirectory
	removeEmptyDirectory = func(string) error { return errors.New("permission denied") }
	t.Cleanup(func() { removeEmptyDirectory = originalRemoveDirectory })
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err == nil {
		t.Fatal("expected empty directory prune failure")
	}
	if _, err := s.GetByID(ctx, model.ID); err != nil {
		t.Fatalf("directory failure removed Model registration: %v", err)
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

func TestDeleteFilesPrunesEmptyOwnedDirectories(t *testing.T) {
	ctx := context.Background()
	s, root := testModelService(t)
	modelDir := filepath.Join(root, "huggingface", "author", "repo")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := writeGGUF(t, modelDir, "model-Q4_K_M.gguf")
	helper := writeGGUF(t, modelDir, "model-mmproj.gguf")
	otherRepo := filepath.Join(root, "huggingface", "author", "other")
	if err := os.MkdirAll(otherRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	neighbor := writeGGUF(t, otherRepo, "keep.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Owned nested", GGUFPath: main, Options: map[string]string{"mmproj": helper}})
	if err != nil {
		t.Fatal(err)
	}
	linkDownloadArtifacts(t, s, model.ID, "job-nested-owned", main, helper)

	plan, err := s.PrepareFileDeletion(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(main); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned primary still exists: %v", err)
	}
	if _, err := os.Stat(helper); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned companion still exists: %v", err)
	}
	if _, err := os.Stat(modelDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty owned repo directory still exists: %v", err)
	}
	if _, err := os.Stat(neighbor); err != nil {
		t.Fatalf("sibling repo file was removed: %v", err)
	}
	if _, err := os.Stat(otherRepo); err != nil {
		t.Fatalf("non-empty sibling repo was removed: %v", err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Fatalf("models root was removed: info=%v err=%v", info, err)
	}
}

func TestPruneEmptyAncestorsDoesNotFollowSymlinkedDirectory(t *testing.T) {
	s, root := testModelService(t)
	realDir := filepath.Join(root, "real-empty")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias-empty")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := s.pruneEmptyAncestors(alias); err != nil {
		t.Fatalf("symlink prune should stop without error: %v", err)
	}
	if _, err := os.Stat(realDir); err != nil {
		t.Fatalf("symlinked real directory was removed: %v", err)
	}
	if _, err := os.Lstat(alias); err != nil {
		t.Fatalf("directory symlink was removed: %v", err)
	}
}
