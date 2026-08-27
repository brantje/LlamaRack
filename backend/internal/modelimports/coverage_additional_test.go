package modelimports

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/downloads"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

func TestPrepareReusesCompletedModelAndStartsImmediately(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	rel := "huggingface/acme/demo/model-Q4_K_M.gguf"
	absolute := filepath.Join(modelsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "Existing", GGUFPath: rel})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,error,created_at,updated_at)
VALUES('done','huggingface','acme/demo','rev','a','model-Q4_K_M.gguf','Q4_K_M',?,1,1,'',unixepoch()-2,unixepoch()-2)`, downloads.StateCompleted)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('done','model-Q4_K_M.gguf',1,?,1,0,?)`, downloads.StateCompleted, rel)
	if err != nil {
		t.Fatal(err)
	}
	starter := &starterSpy{}
	service.starter = starter
	artifact := huggingface.Artifact{
		ID: "a", Name: "model-Q4_K_M.gguf", Quantization: "Q4_K_M", ModelBytes: 1, TotalBytes: 1,
		ShardCount: 1, ExpectedShards: 1, Complete: true, Files: []huggingface.File{{Path: "model-Q4_K_M.gguf", Size: 1}},
	}
	result, err := service.Prepare(ctx, huggingface.ModelDetail{ID: "acme/demo", Revision: "rev"}, artifact, PrepareInput{
		Name: "Reuse", FirstInstance: FirstInstanceInput{Name: "Reuse", Slug: "reuse", AutoloadEnabled: true, EvictionEnabled: true, Start: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Model.ID != model.ID || !result.Instance.Enabled || starter.count() != 1 {
		t.Fatalf("result=%+v starter=%d", result, starter.count())
	}
	items, err := modelService.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("models=%+v err=%v", items, err)
	}
}

func TestRegisterUnclaimedCompletedBranches(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	rel := "huggingface/acme/demo/existing.gguf"
	absolute := filepath.Join(modelsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "Existing", GGUFPath: rel})
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range []struct{ id, name string }{{"existing-job", "existing.gguf"}, {"missing-file-row", "missing.gguf"}, {"empty-local", "empty.gguf"}} {
		_, err := db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,total_bytes,downloaded_bytes,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,1,1,unixepoch()-2,unixepoch()-2)`, job.id, "huggingface", "acme/demo", "rev", job.id+"-artifact", job.name, downloads.StateCompleted)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('existing-job','existing.gguf',1,?,1,0,?)`, downloads.StateCompleted, rel)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('empty-local','empty.gguf',1,?,1,0,'')`, downloads.StateCompleted)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_imports WHERE job_id='existing-job' AND model_id=?`, model.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing import count=%d err=%v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_imports WHERE job_id IN ('missing-file-row','empty-local')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("skipped imports count=%d err=%v", count, err)
	}
}

func TestArtifactOptionsAndPathValidationBranches(t *testing.T) {
	_, modelsDir, _, _, _, service := newImportFixture(t, http.NotFoundHandler())
	artifact := huggingface.Artifact{Dependencies: []huggingface.ArtifactDependency{
		{Kind: "mmproj"},
		{Kind: "unknown", Files: []huggingface.File{{Path: "ignored.gguf"}}},
		{Kind: "mmproj", Files: []huggingface.File{{Path: "vision/mmproj.gguf"}}},
		{Kind: "mmproj", Files: []huggingface.File{{Path: "vision/other-mmproj.gguf"}}},
		{Kind: "mtp", Files: []huggingface.File{{Path: "draft/mtp.gguf"}}},
		{Kind: "mtp", Files: []huggingface.File{{Path: "draft/other-mtp.gguf"}}},
	}}
	options, err := service.artifactOptions(artifact)
	if err != nil {
		t.Fatal(err)
	}
	wantProjector, _ := filepath.Abs(filepath.Join(modelsDir, "vision", "mmproj.gguf"))
	wantDraft, _ := filepath.Abs(filepath.Join(modelsDir, "draft", "mtp.gguf"))
	if filepath.Clean(options["mmproj"]) != filepath.Clean(wantProjector) || filepath.Clean(options["spec-draft-model"]) != filepath.Clean(wantDraft) || options["spec-type"] != "draft-mtp" {
		t.Fatalf("options=%+v", options)
	}
	artifact.Dependencies = []huggingface.ArtifactDependency{{Kind: "mmproj", Files: []huggingface.File{{Path: "../escape.gguf"}}}}
	if _, err := service.artifactOptions(artifact); err == nil {
		t.Fatal("expected unsafe dependency error")
	}
	for _, value := range []string{"/root.gguf", "back\\slash.gguf", "../escape.gguf", "weights.bin", "a/../b.gguf"} {
		if _, err := expectedProviderPathFromRelative(value); err == nil {
			t.Fatalf("provider path %q unexpectedly accepted", value)
		}
	}
}

func TestEnsureModelFileAndLookupErrorBranches(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := os.Mkdir(filepath.Join(modelsDir, "directory.gguf"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,context_length) VALUES('directory-model','Directory','directory.gguf',0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ensureModelFile("directory-model"); err == nil {
		t.Fatal("expected directory model error")
	}
	if err := service.ensureModelFile("missing-model"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing model error=%v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,context_length) VALUES('escape-model','Escape','../escape.gguf',0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ensureModelFile("escape-model"); err == nil {
		t.Fatal("expected escaped model path error")
	}
	if model, found, err := service.modelByPath(ctx, "directory.gguf"); err != nil || !found || model.ID != "directory-model" {
		t.Fatalf("lookup model=%+v found=%v err=%v", model, found, err)
	}
	if _, found, err := service.modelByPath(ctx, "does-not-exist.gguf"); err != nil || found {
		t.Fatalf("unexpected lookup found=%v err=%v", found, err)
	}
	_ = modelService
}

func TestRepairOptionsInsertsMissingHelpersAndCleanupEmptyJob(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := os.WriteFile(filepath.Join(modelsDir, "base.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "Base", GGUFPath: "base.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	artifact := huggingface.Artifact{Dependencies: []huggingface.ArtifactDependency{
		{Kind: "unknown", Files: []huggingface.File{{Path: "ignored.gguf"}}},
		{Kind: "mmproj", Files: []huggingface.File{{Path: "vision/mmproj.gguf"}}},
		{Kind: "mtp", Files: []huggingface.File{{Path: "draft/mtp.gguf"}}},
	}}
	if err := service.RepairArtifactOptions(ctx, model.ID, "acme/demo", artifact); err != nil {
		t.Fatal(err)
	}
	options, err := modelService.Options(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantProjector, _ := filepath.Abs(filepath.Join(modelsDir, "huggingface", "acme", "demo", "vision", "mmproj.gguf"))
	wantDraft, _ := filepath.Abs(filepath.Join(modelsDir, "huggingface", "acme", "demo", "draft", "mtp.gguf"))
	if filepath.Clean(options["mmproj"]) != filepath.Clean(wantProjector) || filepath.Clean(options["spec-draft-model"]) != filepath.Clean(wantDraft) || options["spec-type"] != "draft-mtp" {
		t.Fatalf("repaired options=%+v", options)
	}
	if err := service.RepairArtifactOptions(ctx, model.ID, "acme/demo", artifact); err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupJob(ctx, "no-imports"); err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupJobSafe(ctx, "no-imports"); err != nil {
		t.Fatal(err)
	}
	_ = db
}

func TestModelLookupReturnsDatabaseError(t *testing.T) {
	ctx, _, db, _, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.modelByPath(ctx, "model.gguf"); err == nil {
		t.Fatal("expected closed database error")
	}
}

func TestRunReconcilesOnTicker(t *testing.T) {
	_, _, _, _, _, service := newImportFixture(t, http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.Run(ctx, time.Millisecond)
		close(done)
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit")
	}
}
