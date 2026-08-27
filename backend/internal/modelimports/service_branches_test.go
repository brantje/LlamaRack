package modelimports

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/downloads"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/instances"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

func TestPrepareValidationBranches(t *testing.T) {
	ctx, _, _, _, _, service := newImportFixture(t, http.NotFoundHandler())
	complete := huggingface.Artifact{ShardCount: 1, ExpectedShards: 1, Complete: true, Files: []huggingface.File{{Path: "model.gguf", Size: 1}}}
	base := PrepareInput{Name: "Demo", FirstInstance: FirstInstanceInput{Name: "Demo", Slug: "demo"}}
	cases := []struct {
		name     string
		artifact huggingface.Artifact
		input    PrepareInput
	}{
		{"missing model name", complete, PrepareInput{FirstInstance: FirstInstanceInput{Name: "Demo", Slug: "demo"}}},
		{"negative context", complete, PrepareInput{Name: "Demo", ContextLength: -1, FirstInstance: base.FirstInstance}},
		{"missing instance name", complete, PrepareInput{Name: "Demo", FirstInstance: FirstInstanceInput{Slug: "demo"}}},
		{"missing instance slug", complete, PrepareInput{Name: "Demo", FirstInstance: FirstInstanceInput{Name: "Demo"}}},
		{"incomplete artifact", huggingface.Artifact{ShardCount: 1, ExpectedShards: 1, Complete: false}, base},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.Prepare(ctx, huggingface.ModelDetail{ID: "acme/demo", Revision: "rev"}, tc.artifact, tc.input); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLegacyListAndCleanupOwnedImport(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := os.WriteFile(filepath.Join(modelsDir, "owned.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "Owned", GGUFPath: "owned.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	instance, err := service.instances.Create(ctx, instances.CreateInput{ModelID: model.ID, Name: "Owned", Slug: "owned", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,error) VALUES('legacy-job','huggingface','acme/demo','rev','a','owned.gguf',?,'cancelled')`, downloads.StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state,error) VALUES('legacy-import','legacy-job',?,?,1,1,?,'cancelled')`, model.ID, instance.ID, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	items, err := service.List(ctx)
	if err != nil || len(items) != 1 || items[0].State != StateCancelled || !items[0].StartWhenReady {
		t.Fatalf("legacy statuses=%+v err=%v", items, err)
	}
	if err := service.CleanupJob(ctx, "legacy-job"); err != nil {
		t.Fatal(err)
	}
	if _, err := modelService.GetByID(ctx, model.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("owned model remains, err=%v", err)
	}
}

func TestReconcileFailureCancelledAndMissingFileBranches(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	makeImport := func(id, downloadState, modelPath string, createFile bool) string {
		t.Helper()
		if createFile {
			absolute := filepath.Join(modelsDir, filepath.FromSlash(modelPath))
			if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absolute, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		model, err := modelService.Create(ctx, models.CreateModelInput{Name: id, GGUFPath: modelPath})
		if err != nil {
			// Pending missing-file models cannot use normal Create.
			model = models.Model{ID: id + "-model", Name: id, GGUFPath: modelPath}
			if _, insertErr := db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,context_length) VALUES(?,?,?,1,0)`, model.ID, model.Name, model.GGUFPath); insertErr != nil {
				t.Fatal(insertErr)
			}
		}
		enabled := false
		instance, err := service.instances.Create(ctx, instances.CreateInput{ModelID: model.ID, Name: id, Slug: id, Enabled: &enabled})
		if err != nil {
			t.Fatal(err)
		}
		jobID := id + "-job"
		_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,error) VALUES(?,?,?,?,?,?,?,?)`, jobID, "huggingface", "acme/demo", "rev", "a", filepath.Base(modelPath), downloadState, "provider error")
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state) VALUES(?,?,?,?,0,0,?)`, id+"-import", jobID, model.ID, instance.ID, StateDownloading)
		if err != nil {
			t.Fatal(err)
		}
		return instance.ID
	}

	failedID := makeImport("failed", downloads.StateFailed, "failed.gguf", true)
	cancelledID := makeImport("cancelled", downloads.StateCancelled, "cancelled.gguf", true)
	missingID := makeImport("missing", downloads.StateCompleted, "missing.gguf", false)
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	statuses, err := service.ListResolved(ctx)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, status := range statuses {
		states[status.InstanceID] = status.State
	}
	if states[failedID] != StateFailed || states[cancelledID] != StateCancelled || states[missingID] != StateFailed {
		t.Fatalf("states = %+v", states)
	}
}

func TestReconcileRecordsStartFailureOnlyOnce(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	if err := os.WriteFile(filepath.Join(modelsDir, "ready.gguf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "Ready", GGUFPath: "ready.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	instance, err := service.instances.Create(ctx, instances.CreateInput{ModelID: model.ID, Name: "Ready", Slug: "ready", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state) VALUES('ready-job','huggingface','acme/demo','rev','a','ready.gguf',?)`, downloads.StateCompleted)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state,start_attempted) VALUES('ready-import','ready-job',?,?,0,1,?,0)`, model.ID, instance.ID, StateDownloading)
	if err != nil {
		t.Fatal(err)
	}
	starter := &starterSpy{err: errors.New("no capacity")}
	service.starter = starter
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	statuses, err := service.ListResolved(ctx)
	if err != nil || len(statuses) != 1 || statuses[0].State != StateCompleted || !strings.Contains(statuses[0].Error, "no capacity") {
		t.Fatalf("status=%+v err=%v", statuses, err)
	}
	if starter.count() != 1 {
		t.Fatalf("starter calls=%d", starter.count())
	}
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if starter.count() != 1 {
		t.Fatalf("starter retried unexpectedly: %d", starter.count())
	}
}

func TestRunReturnsWhenContextCancelled(t *testing.T) {
	_, _, _, _, _, service := newImportFixture(t, http.NotFoundHandler())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		service.Run(ctx, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

func TestHelperBranches(t *testing.T) {
	if got := safeComponent("a/b:c"); got != "a_b_c" {
		t.Fatalf("safe component=%q", got)
	}
	if got := safeComponent("///"); got != "___" {
		t.Fatalf("safe empty component=%q", got)
	}
	if got := defaultModelName("single", "", "fallback.gguf"); got != "fallback" {
		t.Fatalf("fallback model name=%q", got)
	}
	if got := defaultModelName("acme/demo", "", "   "); got != "demo" {
		t.Fatalf("repo fallback=%q", got)
	}
	if boolInt(false) != 0 || boolInt(true) != 1 || nullable("") != nil || nullable("x") != "x" {
		t.Fatal("primitive helper mismatch")
	}
	if _, err := expectedProviderPath("invalid", "model.gguf"); err == nil {
		t.Fatal("expected invalid repo error")
	}
}
