package modelimports

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/downloads"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/instances"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

func TestPrepareCleansPendingModelWhenInstanceSlugConflicts(t *testing.T) {
	ctx, modelsDir, _, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := os.WriteFile(filepath.Join(modelsDir, "existing.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	existing, err := modelService.Create(ctx, models.CreateModelInput{Name: "Existing", GGUFPath: "existing.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.instances.Create(ctx, instances.CreateInput{ModelID: existing.ID, Name: "Conflict", Slug: "conflict"}); err != nil {
		t.Fatal(err)
	}
	artifact := huggingface.Artifact{
		ID: "new-artifact", Name: "new-Q4.gguf", ModelBytes: 1, TotalBytes: 1,
		ShardCount: 1, ExpectedShards: 1, Complete: true,
		Files: []huggingface.File{{Path: "new-Q4.gguf", Size: 1}},
	}
	_, err = service.Prepare(ctx, huggingface.ModelDetail{ID: "acme/new", Revision: "rev"}, artifact, PrepareInput{
		Name: "New", FirstInstance: FirstInstanceInput{Name: "Conflict", Slug: "conflict"},
	})
	if err == nil {
		t.Fatal("expected duplicate instance slug error")
	}
	items, err := modelService.List(ctx)
	if err != nil || len(items) != 1 || items[0].ID != existing.ID {
		t.Fatalf("pending model was not rolled back: %+v err=%v", items, err)
	}
}

func TestPrepareReturnsDownloadValidationErrors(t *testing.T) {
	ctx, _, _, _, _, service := newImportFixture(t, http.NotFoundHandler())
	artifact := huggingface.Artifact{
		ID: "artifact", Name: "model.gguf", ModelBytes: 1, TotalBytes: 1,
		ShardCount: 1, ExpectedShards: 1, Complete: true,
		Files: []huggingface.File{{Path: "../escape.gguf", Size: 1}},
	}
	_, err := service.Prepare(ctx, huggingface.ModelDetail{ID: "acme/demo", Revision: "rev"}, artifact, PrepareInput{
		Name: "Demo", FirstInstance: FirstInstanceInput{Name: "Demo", Slug: "demo"},
	})
	if err == nil {
		t.Fatal("expected unsafe provider path error")
	}
}

func TestReconcileQueuedImportAndUnclaimableCompletedDownload(t *testing.T) {
	ctx, _, db, _, _, service := newImportFixture(t, http.NotFoundHandler())
	_, err := db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,context_length) VALUES('pending-model','Pending','pending.gguf',1,0)`)
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	instance, err := service.instances.Create(ctx, instances.CreateInput{ModelID: "pending-model", Name: "Pending", Slug: "pending", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,error,created_at,updated_at) VALUES('queued-job','huggingface','acme/demo','rev','a','pending.gguf',?,'old',unixepoch(),unixepoch())`, downloads.StateQueued)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state,error) VALUES('queued-import','queued-job','pending-model',?,0,0,?,'old')`, instance.ID, StateFailed)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,total_bytes,downloaded_bytes,created_at,updated_at) VALUES('missing-complete','huggingface','acme/missing','rev','b','missing.gguf',?,1,1,unixepoch()-3,unixepoch()-3)`, downloads.StateCompleted)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,state,downloaded_bytes,ordinal,local_path) VALUES('missing-complete','missing.gguf',1,?,1,0,'huggingface/acme/missing/missing.gguf')`, downloads.StateCompleted)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	statuses, err := service.ListResolved(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var queued *Status
	for i := range statuses {
		if statuses[i].InstanceID == instance.ID {
			queued = &statuses[i]
		}
	}
	if queued == nil || queued.State != StateDownloading || queued.Error != "" {
		t.Fatalf("queued status = %+v", queued)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_imports WHERE job_id='missing-complete'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unclaimable completed job registered unexpectedly: count=%d err=%v", count, err)
	}
}

func TestCleanupJobSafeRemovesOwnedModelAndInstance(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := os.WriteFile(filepath.Join(modelsDir, "owned-safe.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "Owned Safe", GGUFPath: "owned-safe.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := service.instances.Create(ctx, instances.CreateInput{ModelID: model.ID, Name: "Owned Safe", Slug: "owned-safe"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state) VALUES('owned-safe-job','huggingface','acme/demo','rev','a','owned-safe.gguf',?)`, downloads.StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state) VALUES('owned-safe-import','owned-safe-job',?,?,1,0,?)`, model.ID, instance.ID, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupJobSafe(ctx, "owned-safe-job"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.instances.Get(ctx, instance.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("instance still exists: %v", err)
	}
	if _, err := modelService.GetByID(ctx, model.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("owned model still exists: %v", err)
	}
}

func TestRepairArtifactOptionsSkipsUnsupportedAndRejectsUnsafePaths(t *testing.T) {
	ctx, modelsDir, _, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := os.WriteFile(filepath.Join(modelsDir, "base-repair.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "Base", GGUFPath: "base-repair.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RepairArtifactOptions(ctx, model.ID, "acme/demo", huggingface.Artifact{Dependencies: []huggingface.ArtifactDependency{
		{Kind: "mmproj"},
		{Kind: "other", Files: []huggingface.File{{Path: "ignored.gguf"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := service.RepairArtifactOptions(ctx, model.ID, "acme/demo", huggingface.Artifact{Dependencies: []huggingface.ArtifactDependency{
		{Kind: "mmproj", Files: []huggingface.File{{Path: "../escape.gguf"}}},
	}}); err == nil {
		t.Fatal("expected unsafe repair path error")
	}
}

func TestImportDatabaseErrorBranches(t *testing.T) {
	ctx, _, db, _, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		fn   func() error
	}{
		{"list", func() error { _, err := service.List(ctx); return err }},
		{"resolved", func() error { _, err := service.ListResolved(ctx); return err }},
		{"cleanup", func() error { return service.CleanupJob(ctx, "x") }},
		{"cleanup safe", func() error { return service.CleanupJobSafe(ctx, "x") }},
		{"reconcile", func() error { return service.Reconcile(ctx) }},
		{"repair", func() error { return service.RepairArtifactOptions(ctx, "m", "acme/demo", huggingface.Artifact{Dependencies: []huggingface.ArtifactDependency{{Kind: "mmproj", Files: []huggingface.File{{Path: "mmproj.gguf"}}}}}) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.fn(); err == nil {
				t.Fatal("expected closed database error")
			}
		})
	}
}

func TestCreatePendingModelRejectsDuplicatePathAndUnsafeHelper(t *testing.T) {
	ctx, modelsDir, _, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := os.WriteFile(filepath.Join(modelsDir, "duplicate.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := modelService.Create(ctx, models.CreateModelInput{Name: "Duplicate", GGUFPath: "duplicate.gguf"}); err != nil {
		t.Fatal(err)
	}
	artifact := huggingface.Artifact{ModelBytes: 1, Dependencies: []huggingface.ArtifactDependency{{Kind: "mmproj", Files: []huggingface.File{{Path: "mmproj.gguf"}}}}}
	if _, err := service.createPendingModel(ctx, "duplicate.gguf", artifact, "Duplicate 2", 0, nil); err == nil {
		t.Fatal("expected duplicate model path error")
	}
	artifact.Dependencies[0].Files[0].Path = "../unsafe.gguf"
	if _, err := service.createPendingModel(ctx, "new.gguf", artifact, "Unsafe", 0, nil); err == nil {
		t.Fatal("expected unsafe helper path error")
	}
}
