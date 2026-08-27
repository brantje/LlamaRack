package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactCandidateBranches(t *testing.T) {
	s, root := testModelService(t)

	if _, err := s.artifactCandidate(""); err == nil {
		t.Fatal("expected empty artifact path to fail")
	}
	if _, err := s.artifactCandidate(filepath.Join(t.TempDir(), "outside.gguf")); err == nil {
		t.Fatal("expected artifact outside models root to fail")
	}

	candidate, err := s.artifactCandidate(filepath.ToSlash(filepath.Join("nested", "model.gguf")))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "nested", "model.gguf")
	if candidate != want {
		t.Fatalf("candidate=%q want=%q", candidate, want)
	}
}

func TestResolveModelDirectorySafetyBranches(t *testing.T) {
	s, root := testModelService(t)

	rootPlan, err := s.resolveModelDirectory(artifactFile{absolutePath: filepath.Join(root, "model.gguf")})
	if err != nil {
		t.Fatal(err)
	}
	if rootPlan != nil {
		t.Fatalf("configured models root must not be a recursive target: %+v", rootPlan)
	}

	outside := filepath.Join(t.TempDir(), "model.gguf")
	if _, err := s.resolveModelDirectory(artifactFile{absolutePath: outside}); !errors.Is(err, ErrUnsafeArtifactPath) {
		t.Fatalf("expected outside directory rejection, got %v", err)
	}

	missingRoot := New(s.db, filepath.Join(root, "missing-model-root"))
	if _, err := missingRoot.resolveModelDirectory(artifactFile{absolutePath: filepath.Join(root, "missing-model-root", "nested", "model.gguf")}); err == nil {
		t.Fatal("expected missing models root resolution to fail")
	}

	primary, err := s.resolveArtifactFile(artifactReference{path: filepath.ToSlash(filepath.Join("missing", "nested", "model.gguf"))})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.resolveModelDirectory(primary)
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.relativePath != filepath.ToSlash(filepath.Join("missing", "nested")) {
		t.Fatalf("unexpected missing directory plan: %+v", plan)
	}

	notDirectory := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(notDirectory, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveModelDirectory(artifactFile{absolutePath: filepath.Join(notDirectory, "model.gguf")}); !errors.Is(err, ErrUnsafeArtifactPath) {
		t.Fatalf("expected non-directory model parent rejection, got %v", err)
	}
}

func TestEnsureNoSymlinkComponentsBranches(t *testing.T) {
	root := t.TempDir()

	if err := ensureNoSymlinkComponents(root, root); err != nil {
		t.Fatalf("root should be accepted: %v", err)
	}
	if err := ensureNoSymlinkComponents(root, filepath.Join(root, "missing", "nested")); err != nil {
		t.Fatalf("missing nested path should be accepted: %v", err)
	}

	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := ensureNoSymlinkComponents(root, filepath.Join(link, "nested")); err == nil {
		t.Fatal("expected symlink component rejection")
	}
}

func TestDeleteFilesAndModelAllowsAlreadyMissingNestedFolder(t *testing.T) {
	ctx := context.Background()
	s, root := testModelService(t)
	modelDir := filepath.Join(root, "already-gone")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	main := writeGGUF(t, modelDir, "model.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Already gone", GGUFPath: main})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(modelDir); err != nil {
		t.Fatal(err)
	}

	plan, err := s.PrepareFileDeletion(ctx, model.ID)
	if err != nil {
		t.Fatalf("missing model folder should be treated as already deleted: %v", err)
	}
	if plan.directory == nil {
		t.Fatal("expected nested directory deletion plan")
	}
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByID(ctx, model.ID); err == nil {
		t.Fatal("model registration still exists")
	}
}
