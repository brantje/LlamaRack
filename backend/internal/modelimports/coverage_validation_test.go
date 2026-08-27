package modelimports

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/downloads"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

func TestPrepareValidationBranches(t *testing.T) {
	ctx, _, _, _, _, service := newImportFixture(t, http.NotFoundHandler())
	complete := huggingface.Artifact{
		ID: "artifact", Name: "model.gguf", ModelBytes: 1, TotalBytes: 1,
		ShardCount: 1, ExpectedShards: 1, Complete: true,
		Files: []huggingface.File{{Path: "model.gguf", Size: 1}},
	}
	base := PrepareInput{
		Name: "Demo",
		FirstInstance: FirstInstanceInput{Name: "Demo", Slug: "demo"},
	}
	tests := []struct {
		name     string
		input    PrepareInput
		artifact huggingface.Artifact
	}{
		{"missing model name", PrepareInput{FirstInstance: base.FirstInstance}, complete},
		{"negative context", PrepareInput{Name: "Demo", ContextLength: -1, FirstInstance: base.FirstInstance}, complete},
		{"missing instance name", PrepareInput{Name: "Demo", FirstInstance: FirstInstanceInput{Slug: "demo"}}, complete},
		{"missing instance slug", PrepareInput{Name: "Demo", FirstInstance: FirstInstanceInput{Name: "Demo"}}, complete},
		{"incomplete artifact", base, huggingface.Artifact{ID: "bad", Name: "bad.gguf", Complete: false}},
		{"missing shards", base, huggingface.Artifact{ID: "bad", Name: "bad.gguf", Complete: true, ShardCount: 0}},
		{"short shard list", base, huggingface.Artifact{ID: "bad", Name: "bad.gguf", Complete: true, ShardCount: 2, Files: []huggingface.File{{Path: "one.gguf"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.Prepare(ctx, huggingface.ModelDetail{ID: "acme/demo", Revision: "rev"}, tc.artifact, tc.input); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestRepairArtifactOptionsInsertsMissingHelpers(t *testing.T) {
	ctx, modelsDir, _, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := os.WriteFile(filepath.Join(modelsDir, "repair-main.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "Repair", GGUFPath: "repair-main.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	artifact := huggingface.Artifact{Dependencies: []huggingface.ArtifactDependency{
		{Kind: "mmproj", Files: []huggingface.File{{Path: "vision/mmproj-F16.gguf"}}},
		{Kind: "mtp", Files: []huggingface.File{{Path: "draft/mtp-Q4.gguf"}}},
	}}
	if err := service.RepairArtifactOptions(ctx, model.ID, "acme/demo", artifact); err != nil {
		t.Fatal(err)
	}
	options, err := modelService.Options(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantMMProj, _ := filepath.Abs(filepath.Join(modelsDir, "huggingface", "acme", "demo", "vision", "mmproj-F16.gguf"))
	wantMTP, _ := filepath.Abs(filepath.Join(modelsDir, "huggingface", "acme", "demo", "draft", "mtp-Q4.gguf"))
	if filepath.Clean(options["mmproj"]) != filepath.Clean(wantMMProj) {
		t.Fatalf("mmproj=%q want=%q", options["mmproj"], wantMMProj)
	}
	if filepath.Clean(options["spec-draft-model"]) != filepath.Clean(wantMTP) || options["spec-type"] != "draft-mtp" {
		t.Fatalf("MTP options=%+v", options)
	}
}

func TestCleanupJobAndSafeCleanupNoOwnedResources(t *testing.T) {
	ctx, _, db, _, _, service := newImportFixture(t, http.NotFoundHandler())
	_, err := db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,context_length) VALUES('existing-model','Existing','existing.gguf',1,0)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state) VALUES('plain-job','huggingface','acme/demo','rev','a','plain.gguf',?)`, downloads.StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state) VALUES('plain-import','plain-job','existing-model',NULL,0,0,?)`, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupJob(ctx, "plain-job"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_imports WHERE job_id='plain-job'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("plain import rows=%d err=%v", count, err)
	}

	_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state) VALUES('tombstone-job','huggingface','acme/demo','rev','b','tombstone.gguf',?)`, downloads.StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state) VALUES('tombstone-import','tombstone-job',NULL,NULL,1,0,?)`, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupJobSafe(ctx, "tombstone-job"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_imports WHERE job_id='tombstone-job'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("tombstone import rows=%d err=%v", count, err)
	}
}
