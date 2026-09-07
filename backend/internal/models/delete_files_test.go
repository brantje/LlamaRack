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
	linkDownloadArtifacts(t, s, model.ID, "job-split", shard1, shard2)

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

func TestDeleteFilesKeepsUnownedCompanionAndAllowsMissingFiles(t *testing.T) {
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
	if _, err := os.Stat(helper); err != nil {
		t.Fatalf("unowned referenced companion was removed: %v", err)
	}
}

func TestDeleteFilesRemovesOwnedCompanionsFromDownloadJob(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	main := writeGGUF(t, dir, "vision.gguf")
	projector := writeGGUF(t, dir, "vision-mmproj.gguf")
	draft := writeGGUF(t, dir, "vision-mtp.gguf")
	model, err := s.Create(ctx, CreateModelInput{
		Name: "Vision", GGUFPath: main,
		Options: map[string]string{"mmproj": projector, "spec-draft-model": draft},
	})
	if err != nil {
		t.Fatal(err)
	}
	linkDownloadArtifacts(t, s, model.ID, "job-owned-helpers", main, projector, draft)

	plan, err := s.PrepareFileDeletion(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{main, projector, draft} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned companion %q still exists: %v", path, err)
		}
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
	linkDownloadArtifacts(t, s, model.ID, "job-symlink-helper", main, link)
	if _, err := s.PrepareFileDeletion(ctx, model.ID); !errors.Is(err, ErrUnsafeArtifactPath) {
		t.Fatalf("expected symlink target rejection, got %v", err)
	}
}

func TestPrepareFileDeletionAllowsSharedUnownedCompanion(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	shared := writeGGUF(t, dir, "shared-mmproj.gguf")
	firstPath := writeGGUF(t, dir, "first.gguf")
	first, err := s.Create(ctx, CreateModelInput{Name: "First", GGUFPath: firstPath, Options: map[string]string{"mmproj": shared}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateModelInput{Name: "Second", GGUFPath: writeGGUF(t, dir, "second.gguf"), Options: map[string]string{"mmproj": shared}}); err != nil {
		t.Fatal(err)
	}
	plan, err := s.PrepareFileDeletion(ctx, first.ID)
	if err != nil {
		t.Fatalf("unowned shared companion should not block deletion: %v", err)
	}
	if err := s.DeleteFilesAndModel(ctx, first.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("unowned shared companion was removed: %v", err)
	}
	if _, err := os.Stat(firstPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("primary file still exists: %v", err)
	}
}

func TestPrepareFileDeletionRefusesSharedOwnedCompanion(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	shared := writeGGUF(t, dir, "shared-mmproj.gguf")
	firstPath := writeGGUF(t, dir, "first.gguf")
	first, err := s.Create(ctx, CreateModelInput{Name: "First", GGUFPath: firstPath, Options: map[string]string{"mmproj": shared}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateModelInput{Name: "Second", GGUFPath: writeGGUF(t, dir, "second.gguf"), Options: map[string]string{"mmproj": shared}}); err != nil {
		t.Fatal(err)
	}
	linkDownloadArtifacts(t, s, first.ID, "job-shared-owned", firstPath, shared)
	if _, err := s.PrepareFileDeletion(ctx, first.ID); !errors.Is(err, ErrArtifactShared) {
		t.Fatalf("expected shared owned companion conflict, got %v", err)
	}
	if _, err := s.GetByID(ctx, first.ID); err != nil {
		t.Fatalf("shared conflict removed Model metadata: %v", err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("shared helper was removed: %v", err)
	}
}

func TestPrepareFileDeletionRefusesOwnedCompanionUsedByOtherInstance(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	shared := writeGGUF(t, dir, "instance-mmproj.gguf")
	firstPath := writeGGUF(t, dir, "first.gguf")
	first, err := s.Create(ctx, CreateModelInput{Name: "First", GGUFPath: firstPath})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ctx, CreateModelInput{Name: "Second", GGUFPath: writeGGUF(t, dir, "second.gguf")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO instances(id,slug,model_id,name) VALUES('inst-shared','inst-shared',?,'Second instance')`, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO instance_options(instance_id,option_key,option_value) VALUES('inst-shared','mmproj',?)`, shared); err != nil {
		t.Fatal(err)
	}
	linkDownloadArtifacts(t, s, first.ID, "job-instance-owned", firstPath, shared)
	if _, err := s.PrepareFileDeletion(ctx, first.ID); !errors.Is(err, ErrArtifactShared) {
		t.Fatalf("expected instance companion conflict, got %v", err)
	}
}

func TestPrepareFileDeletionIgnoresCompanionOnOwnInstance(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	helper := writeGGUF(t, dir, "own-mmproj.gguf")
	main := writeGGUF(t, dir, "own-main.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Owned", GGUFPath: main, Options: map[string]string{"mmproj": helper}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO instances(id,slug,model_id,name) VALUES('inst-own','inst-own',?,'Own instance')`, model.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO instance_options(instance_id,option_key,option_value) VALUES('inst-own','mmproj',?)`, helper); err != nil {
		t.Fatal(err)
	}
	linkDownloadArtifacts(t, s, model.ID, "job-own-instance", main, helper)
	plan, err := s.PrepareFileDeletion(ctx, model.ID)
	if err != nil {
		t.Fatalf("own instance companion should not block deletion: %v", err)
	}
	if err := s.DeleteFilesAndModel(ctx, model.ID, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(helper); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned companion still exists: %v", err)
	}
}

func TestPrepareFileDeletionRefusesOwnedCompanionReachedViaSymlink(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	shared := writeGGUF(t, dir, "real-mmproj.gguf")
	alias := filepath.Join(dir, "alias-mmproj.gguf")
	if err := os.Symlink(shared, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	firstPath := writeGGUF(t, dir, "first.gguf")
	first, err := s.Create(ctx, CreateModelInput{Name: "First", GGUFPath: firstPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateModelInput{Name: "Second", GGUFPath: writeGGUF(t, dir, "second.gguf"), Options: map[string]string{"mmproj": alias}}); err != nil {
		t.Fatal(err)
	}
	linkDownloadArtifacts(t, s, first.ID, "job-symlink-shared", firstPath, shared)
	if _, err := s.PrepareFileDeletion(ctx, first.ID); !errors.Is(err, ErrArtifactShared) {
		t.Fatalf("expected symlink companion conflict, got %v", err)
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
