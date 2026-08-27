package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveArtifactFileSafetyBranches(t *testing.T) {
	s, dir := testModelService(t)

	for name, ref := range map[string]artifactReference{
		"empty":    {},
		"non-gguf": {path: "notes.txt"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.resolveArtifactFile(ref); !errors.Is(err, ErrUnsafeArtifactPath) {
				t.Fatalf("expected unsafe path error, got %v", err)
			}
		})
	}

	directory := filepath.Join(dir, "directory.gguf")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveArtifactFile(artifactReference{path: directory}); !errors.Is(err, ErrUnsafeArtifactPath) {
		t.Fatalf("expected non-regular file rejection, got %v", err)
	}

	outside := t.TempDir()
	outsideFile := writeGGUF(t, outside, "outside.gguf")
	linkedDir := filepath.Join(dir, "linked")
	if err := os.Symlink(outside, linkedDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := s.resolveArtifactFile(artifactReference{path: filepath.Join(linkedDir, filepath.Base(outsideFile))}); !errors.Is(err, ErrUnsafeArtifactPath) {
		t.Fatalf("expected parent symlink escape rejection, got %v", err)
	}
}

func TestExistingAncestorClimbsMissingDirectories(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "one", "two", "three")
	ancestor, real, err := existingAncestor(missing)
	if err != nil {
		t.Fatal(err)
	}
	if ancestor != root {
		t.Fatalf("ancestor=%q want=%q", ancestor, root)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if real != rootReal {
		t.Fatalf("real ancestor=%q want=%q", real, rootReal)
	}
}

func TestDeleteFilesPlanAndReferenceEdgeCases(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	main := writeGGUF(t, dir, "edge.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Edge", GGUFPath: main})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteFilesAndModel(ctx, model.ID, FileDeletePlan{modelID: "different"}); err == nil {
		t.Fatal("expected mismatched deletion plan to fail")
	}

	if _, err := s.db.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES(?, 'mmproj', '')`, model.ID); err != nil {
		t.Fatal(err)
	}
	refs, err := s.artifactReferences(ctx, model)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("empty helper option should be ignored, refs=%v", refs)
	}

	other := writeGGUF(t, dir, "other.gguf")
	otherModel, err := s.Create(ctx, CreateModelInput{Name: "Malformed other", GGUFPath: other})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE models SET gguf_path='../bad.gguf' WHERE id=?`, otherModel.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareFileDeletion(ctx, model.ID); err != nil {
		t.Fatalf("malformed unrelated Model should not block safe deletion: %v", err)
	}
}
