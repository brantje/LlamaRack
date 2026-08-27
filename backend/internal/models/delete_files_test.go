package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteRemainsMetadataOnlyByDefault(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := writeGGUF(t, dir, "metadata-only.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Metadata only", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, model.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("metadata-only deletion removed the GGUF: %v", err)
	}
}

func TestDeleteFilesAndModelRemovesSingleFile(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := writeGGUF(t, dir, "single.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Single", GGUFPath: path})
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
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("model file still exists: %v", err)
	}
	if _, err := s.GetByID(ctx, model.ID); err == nil {
		t.Fatal("model database row still exists")
	}
}

func TestDeleteFilesRemovesPersistedSplitArtifactOnly(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	shard1 := writeGGUF(t, dir, "coder-Q4_K_M-00001-of-00002.gguf")
	shard2 := writeGGUF(t, dir, "coder-Q4_K_M-00002-of-00002.gguf")
	neighbor := writeGGUF(t, dir, "coder-Q4_K_M-backup.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Split", GGUFPath: shard1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,total_bytes) VALUES('job-split','huggingface','owner/repo','main','artifact','split','COMPLETED',2)`); err != nil {
		t.Fatal(err)
	}
	for ordinal, path := range []string{filepath.Base(shard1), filepath.Base(shard2)} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,ordinal,local_path) VALUES('job-split',?,1,'COMPLETED',?,?)`, filepath.Base(path), ordinal, path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,owns_model,start_when_ready,state) VALUES('import-split','job-split',?,1,0,'COMPLETED')`, model.ID); err != nil {
		t.Fatal(err)
	}

	plan, err := s.PrepareFileDeletion(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{shard1, shard2} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("persisted shard %q still exists: %v", path, err)
		}
	}
	if _, err := os.Stat(neighbor); err != nil {
		t.Fatalf("unrelated neighboring file was removed: %v", err)
	}
}

func TestDeleteFilesIncludesExplicitHelperAndAllowsMissingFiles(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	main := writeGGUF(t, dir, "vision.gguf")
	helper := writeGGUF(t, dir, "vision-mmproj.gguf")
	model, err := s.Create(ctx, CreateModelInput{
		Name: "Vision", GGUFPath: main,
		Options: map[string]string{"mmproj": helper},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(main); err != nil {
		t.Fatal(err)
	}
	plan, err := s.PrepareFileDeletion(ctx, model.ID)
	if err != nil {
		t.Fatalf("missing main file should be treated as already deleted: %v", err)
	}
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(helper); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit helper was not removed: %v", err)
	}
}

func TestPrepareFileDeletionRejectsEscapedAndSymlinkTargets(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	main := writeGGUF(t, dir, "safe.gguf")
	outside := writeGGUF(t, t.TempDir(), "outside.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Safe", GGUFPath: main})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE models SET gguf_path='../outside.gguf' WHERE id=?`, model.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareFileDeletion(ctx, model.ID); !errors.Is(err, ErrUnsafeArtifactPath) {
		t.Fatalf("expected escaped path rejection, got %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was touched: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE models SET gguf_path='safe.gguf' WHERE id=?`, model.ID); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "helper.gguf")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO model_options(model_id,option_key,option_value) VALUES(?, 'mmproj', ?)`, model.ID, link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareFileDeletion(ctx, model.ID); !errors.Is(err, ErrUnsafeArtifactPath) {
		t.Fatalf("expected symlink target rejection, got %v", err)
	}
}

func TestPrepareFileDeletionRefusesSharedArtifact(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	shared := writeGGUF(t, dir, "shared-mmproj.gguf")
	first, err := s.Create(ctx, CreateModelInput{Name: "First", GGUFPath: writeGGUF(t, dir, "first.gguf"), Options: map[string]string{"mmproj": shared}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateModelInput{Name: "Second", GGUFPath: writeGGUF(t, dir, "second.gguf"), Options: map[string]string{"mmproj": shared}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareFileDeletion(ctx, first.ID); !errors.Is(err, ErrArtifactShared) {
		t.Fatalf("expected shared artifact conflict, got %v", err)
	}
	if _, err := s.GetByID(ctx, first.ID); err != nil {
		t.Fatalf("shared conflict removed Model metadata: %v", err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("shared helper was removed: %v", err)
	}
}

func TestFilesystemDeleteFailureKeepsModelRegistration(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	main := writeGGUF(t, dir, "io-failure.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "I/O failure", GGUFPath: main})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PrepareFileDeletion(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}

	originalRemove := removeArtifactFile
	removeArtifactFile = func(string) error { return errors.New("permission denied") }
	t.Cleanup(func() { removeArtifactFile = originalRemove })
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err == nil {
		t.Fatal("expected filesystem deletion failure")
	}
	if _, err := s.GetByID(ctx, model.ID); err != nil {
		t.Fatalf("filesystem failure removed Model registration: %v", err)
	}
	if _, err := os.Stat(main); err != nil {
		t.Fatalf("filesystem failure removed model file: %v", err)
	}
}
