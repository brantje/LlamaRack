package modelimports

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/models"
)

type starterSpy struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (s *starterSpy) StartInstance(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, id)
	return "", s.err
}

func (s *starterSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func newImportFixture(t *testing.T, handler http.Handler) (context.Context, string, *sql.DB, *models.Service, *downloads.Manager, *Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	root := t.TempDir()
	modelsDir := filepath.Join(root, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	hf, err := huggingface.NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	modelService := models.New(db, modelsDir)
	downloadManager := downloads.New(ctx, db, modelsDir, hf)
	service := New(db, modelsDir, modelService, downloadManager, nil)
	return ctx, modelsDir, db, modelService, downloadManager, service
}

func TestReconcileNotifiesOnceOnImportCompletion(t *testing.T) {
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/demo/resolve/rev1/demo-Q4_K_M.gguf" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "4")
		if r.Method == http.MethodHead {
			return
		}
		<-release
		_, _ = w.Write([]byte("data"))
	})
	ctx, _, _, _, downloadManager, service := newImportFixture(t, handler)
	var notifyMu sync.Mutex
	notifyCounts := map[string]int{}
	service.SetInstanceOnChange(func(_ context.Context, instanceID string) {
		notifyMu.Lock()
		defer notifyMu.Unlock()
		notifyCounts[instanceID]++
	})
	artifact := huggingface.Artifact{
		ID: "artifact-1", Name: "demo-Q4_K_M.gguf", Quantization: "Q4_K_M",
		ModelBytes: 4, TotalBytes: 4, ShardCount: 1, ExpectedShards: 1, Complete: true,
		Files: []huggingface.File{{Path: "demo-Q4_K_M.gguf", Size: 4, OID: "oid"}},
	}
	result, err := service.Prepare(ctx, huggingface.ModelDetail{ID: "acme/demo", Revision: "rev1"}, artifact, PrepareInput{
		Name: "Demo Q4", ContextLength: 4096,
		FirstInstance: FirstInstanceInput{Name: "Demo Instance", Slug: "demo-instance"},
	})
	if err != nil {
		t.Fatal(err)
	}
	notifyMu.Lock()
	if notifyCounts[result.Instance.ID] != 1 {
		t.Fatalf("expected one notify on create, got %d", notifyCounts[result.Instance.ID])
	}
	notifyMu.Unlock()

	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err := downloadManager.Get(ctx, result.Download.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == downloads.StateCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("download state = %s", job.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	notifyMu.Lock()
	if notifyCounts[result.Instance.ID] != 2 {
		t.Fatalf("expected completion notify, got %d total", notifyCounts[result.Instance.ID])
	}
	notifyMu.Unlock()
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	notifyMu.Lock()
	if notifyCounts[result.Instance.ID] != 2 {
		t.Fatalf("expected no extra notify after completed reconcile, got %d", notifyCounts[result.Instance.ID])
	}
	notifyMu.Unlock()
}

func TestPrepareCreatesDownloadingInstanceAndStartsAfterCompletion(t *testing.T) {
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/demo/resolve/rev1/demo-Q4_K_M.gguf" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", "v1")
		w.Header().Set("X-Linked-Size", "4")
		if r.Method == http.MethodHead {
			return
		}
		<-release
		_, _ = w.Write([]byte("data"))
	})
	ctx, _, _, modelService, downloadManager, service := newImportFixture(t, handler)
	starter := &starterSpy{}
	service.starter = starter
	artifact := huggingface.Artifact{
		ID: "artifact-1", Name: "demo-Q4_K_M.gguf", Quantization: "Q4_K_M",
		ModelBytes: 4, TotalBytes: 4, ShardCount: 1, ExpectedShards: 1, Complete: true,
		Files: []huggingface.File{{Path: "demo-Q4_K_M.gguf", Size: 4, OID: "oid"}},
	}
	result, err := service.Prepare(ctx, huggingface.ModelDetail{ID: "acme/demo", Revision: "rev1"}, artifact, PrepareInput{
		Name: "Demo Q4", ContextLength: 4096,
		FirstInstance: FirstInstanceInput{
			Name: "Demo Instance", Slug: "demo-instance", AlwaysOn: true,
			AutoloadEnabled: true, EvictionEnabled: false, Start: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Model.GGUFPath != "huggingface/acme/demo/demo-Q4_K_M.gguf" {
		t.Fatalf("pending model path = %q", result.Model.GGUFPath)
	}
	if result.Model.ContextLength != 4096 {
		t.Fatalf("context = %d", result.Model.ContextLength)
	}
	instance, err := service.instances.Get(ctx, result.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Enabled {
		t.Fatal("instance should be disabled while the GGUF is downloading")
	}
	if !instance.AlwaysOn || !instance.Autoload || instance.EvictionEnabled {
		t.Fatalf("instance policy = %+v", instance)
	}
	statuses, err := service.ListResolved(ctx)
	if err != nil || len(statuses) != 1 || statuses[0].State != StateDownloading || !statuses[0].StartWhenReady {
		t.Fatalf("statuses = %+v err=%v", statuses, err)
	}
	close(release)

	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err := downloadManager.Get(ctx, result.Download.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == downloads.StateCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("download state = %s", job.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	instance, err = service.instances.Get(ctx, result.Instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !instance.Enabled {
		t.Fatal("instance was not enabled after verified completion")
	}
	if starter.count() != 1 {
		t.Fatalf("starter calls = %d", starter.count())
	}
	statuses, err = service.ListResolved(ctx)
	if err != nil || statuses[0].State != StateCompleted || statuses[0].Error != "" {
		t.Fatalf("completed status = %+v err=%v", statuses, err)
	}
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if starter.count() != 1 {
		t.Fatalf("starter called again: %d", starter.count())
	}
	models, err := modelService.List(ctx)
	if err != nil || len(models) != 1 {
		t.Fatalf("models = %+v err=%v", models, err)
	}
}

func TestReconcileRegistersUnclaimedCompletedDownloadAsModel(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	rel := "huggingface/acme/demo/demo-Q5_K_M.gguf"
	absolute := filepath.Join(modelsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("model"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,speed_bps,error,created_at,updated_at)
VALUES('completed-job','huggingface','acme/demo','rev','artifact','demo-Q5_K_M.gguf','Q5_K_M',?,5,5,0,'',unixepoch()-2,unixepoch()-2)`, downloads.StateCompleted)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_files(job_id,path,size,oid,state,downloaded_bytes,etag,ordinal,local_path)
VALUES('completed-job','demo-Q5_K_M.gguf',5,'oid',?,5,'v1',0,?)`, downloads.StateCompleted, rel)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := modelService.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].GGUFPath != rel || items[0].Quantization != "Q5_K_M" {
		t.Fatalf("registered models = %+v", items)
	}
	if !strings.Contains(items[0].Name, "demo") || !strings.Contains(items[0].Name, "Q5_K_M") {
		t.Fatalf("model name = %q", items[0].Name)
	}
	if err := service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	items, _ = modelService.List(ctx)
	if len(items) != 1 {
		t.Fatalf("reconcile should be idempotent, models=%+v", items)
	}
}

func TestRepairArtifactOptionsUsesRepositoryPathAndPreservesUserValue(t *testing.T) {
	ctx, modelsDir, _, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	artifact := huggingface.Artifact{
		ID: "a", Name: "main-Q4_K_M.gguf", Quantization: "Q4_K_M", ModelBytes: 1, TotalBytes: 3,
		ShardCount: 1, ExpectedShards: 1, Complete: true,
		Files: []huggingface.File{{Path: "main-Q4_K_M.gguf", Size: 1}},
		Dependencies: []huggingface.ArtifactDependency{
			{Kind: "mmproj", Name: "mmproj-F16.gguf", Files: []huggingface.File{{Path: "vision/mmproj-F16.gguf", Size: 1}}},
			{Kind: "mtp", Name: "mtp-Q4.gguf", Files: []huggingface.File{{Path: "draft/mtp-Q4.gguf", Size: 1}}},
		},
	}
	model, err := service.createPendingModel(ctx, "huggingface/acme/demo/main-Q4_K_M.gguf", artifact, "Demo", 0, map[string]string{"mmproj": "/custom/projector.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RepairArtifactOptions(ctx, model.ID, "acme/demo", artifact); err != nil {
		t.Fatal(err)
	}
	options, err := modelService.Options(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if options["mmproj"] != "/custom/projector.gguf" {
		t.Fatalf("custom projector was overwritten: %q", options["mmproj"])
	}
	wantDraft, _ := filepath.Abs(filepath.Join(modelsDir, "huggingface", "acme", "demo", "draft", "mtp-Q4.gguf"))
	if filepath.Clean(options["spec-draft-model"]) != filepath.Clean(wantDraft) || options["spec-type"] != "draft-mtp" {
		t.Fatalf("MTP options = %+v, want draft=%q", options, wantDraft)
	}
}

func TestCleanupJobSafeRemovesPendingInstanceButKeepsExistingModel(t *testing.T) {
	ctx, modelsDir, db, modelService, _, service := newImportFixture(t, http.NotFoundHandler())
	modelFile := filepath.Join(modelsDir, "existing.gguf")
	if err := os.WriteFile(modelFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := modelService.Create(ctx, models.CreateModelInput{Name: "Existing", GGUFPath: "existing.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	enabled := false
	instance, err := service.instances.Create(ctx, instances.CreateInput{ModelID: model.ID, Name: "Pending", Slug: "pending", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,created_at,updated_at)
VALUES('cancelled-job','huggingface','acme/demo','rev','a','main.gguf',?,unixepoch(),unixepoch())`, downloads.StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,instance_id,owns_model,start_when_ready,state)
VALUES('import-1','cancelled-job',?,?,0,0,?)`, model.ID, instance.ID, StateCancelled)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CleanupJobSafe(ctx, "cancelled-job"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.instances.Get(ctx, instance.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending instance still exists, err=%v", err)
	}
	if _, err := modelService.GetByID(ctx, model.ID); err != nil {
		t.Fatalf("existing model was removed: %v", err)
	}
}

func TestHelpersAndResolvedFailureState(t *testing.T) {
	ctx, _, db, _, _, service := newImportFixture(t, http.NotFoundHandler())
	if got, err := expectedProviderPath("acme/demo", "weights/model.gguf"); err != nil || got != "huggingface/acme/demo/weights/model.gguf" {
		t.Fatalf("path=%q err=%v", got, err)
	}
	for _, value := range []string{"", "/root.gguf", "../escape.gguf", "not.ggml"} {
		if _, err := expectedProviderPathFromRelative(value); err == nil {
			t.Fatalf("expected invalid provider path %q", value)
		}
	}
	if defaultModelName("acme/demo", "Q8_0", "ignored.gguf") != "demo Q8_0" {
		t.Fatal("unexpected default model name")
	}
	if publicState(downloads.StateQueued) != StateDownloading || publicState(downloads.StateFailed) != StateFailed || publicState(downloads.StateCancelled) != StateCancelled || publicState(downloads.StateCompleted) != StateCompleted {
		t.Fatal("unexpected public state mapping")
	}
	modelID := "model-state"
	_, err := db.ExecContext(ctx, `INSERT INTO models(id,name,gguf_path,total_bytes,context_length) VALUES(?,?,?,0,0)`, modelID, "State", "state.gguf")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO download_jobs(id,provider,repo_id,revision,artifact_id,name,state,error) VALUES('failed-job','huggingface','acme/demo','rev','a','main.gguf',?,'network')`, downloads.StateCompleted)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO provider_imports(id,job_id,model_id,owns_model,start_when_ready,state,error) VALUES('failed-import','failed-job',?,0,0,?,'verification')`, modelID, StateFailed)
	if err != nil {
		t.Fatal(err)
	}
	items, err := service.ListResolved(ctx)
	if err != nil || len(items) != 1 || items[0].State != StateFailed || items[0].Error != "verification" {
		t.Fatalf("resolved statuses=%+v err=%v", items, err)
	}
}

