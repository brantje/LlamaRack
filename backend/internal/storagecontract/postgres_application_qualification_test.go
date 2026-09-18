package storagecontract_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/auth"
	"github.com/brantje/llamarack/backend/internal/benchmark"
	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/downloads"
	"github.com/brantje/llamarack/backend/internal/huggingface"
	"github.com/brantje/llamarack/backend/internal/modelimports"
	"github.com/brantje/llamarack/backend/internal/models"
	"github.com/brantje/llamarack/backend/internal/observability"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

type qualificationStarter struct {
	mu    sync.Mutex
	calls []string
}

func (s *qualificationStarter) StartInstance(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, id)
	s.mu.Unlock()
	return "qualification-generation", nil
}

func (s *qualificationStarter) called(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls) == 1 && s.calls[0] == id
}

func TestPostgresApplicationPersistenceQualification(t *testing.T) {
	base := os.Getenv("LLAMARACK_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("LLAMARACK_TEST_POSTGRES_URL is not configured")
	}
	ctx := context.Background()
	dsn := isolatedPostgresDSN(t, base)
	open := func() database.Store {
		t.Helper()
		store, err := database.OpenConfigured(ctx, "", dsn)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	store := open()
	modelsDir := t.TempDir()

	// Downloads: create files, persist progress, fail/retry, cancel/remove, and
	// leave a second in-flight job to prove restart persistence.
	downloadStore := downloads.NewDownloadStore(store)
	job := downloads.Job{
		ID: "qualification-download", Provider: "huggingface", RepoID: "acme/download",
		Revision: "rev-1", ArtifactID: "artifact-1", Name: "download.gguf",
		State: downloads.StateQueued, TotalBytes: 30,
	}
	files := []downloads.File{
		{Path: "download-00001-of-00002.gguf", Size: 10, State: downloads.StateQueued, Ordinal: 0},
		{Path: "download-00002-of-00002.gguf", Size: 20, State: downloads.StateQueued, Ordinal: 1},
	}
	if err := downloadStore.Create(ctx, job, files); err != nil {
		t.Fatal(err)
	}
	if err := downloadStore.SetFileDownloading(ctx, job.ID, files[0].Path, 4, "etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := downloadStore.SetTempPath(ctx, job.ID, files[0].Path, "/tmp/qualification.part"); err != nil {
		t.Fatal(err)
	}
	if err := downloadStore.RefreshAggregate(ctx, job.ID, 123); err != nil {
		t.Fatal(err)
	}
	progress, err := downloadStore.Get(ctx, job.ID)
	if err != nil || progress.DownloadedBytes != 4 || progress.SpeedBPS != 123 {
		t.Fatalf("download progress=%+v err=%v", progress, err)
	}
	if err := downloadStore.SetFileState(ctx, job.ID, files[0].Path, downloads.StateFailed); err != nil {
		t.Fatal(err)
	}
	if err := downloadStore.SetJobState(ctx, job.ID, downloads.StateFailed, "qualification failure"); err != nil {
		t.Fatal(err)
	}
	if retried, err := downloadStore.Retry(ctx, job.ID); err != nil || !retried {
		t.Fatalf("retry=%v err=%v", retried, err)
	}
	if changed, state, err := downloadStore.Cancel(ctx, job.ID); err != nil || !changed || state != downloads.StateCancelled {
		t.Fatalf("cancel changed=%v state=%q err=%v", changed, state, err)
	}
	if removed, state, err := downloadStore.RemoveCancelled(ctx, job.ID); err != nil || !removed || state != "" {
		t.Fatalf("remove changed=%v state=%q err=%v", removed, state, err)
	}

	persistedJob := downloads.Job{
		ID: "qualification-persisted-download", Provider: "huggingface", RepoID: "acme/persisted",
		Revision: "rev-2", ArtifactID: "artifact-2", Name: "persisted.gguf",
		State: downloads.StateDownloading, TotalBytes: 100,
	}
	if err := downloadStore.Create(ctx, persistedJob, []downloads.File{{Path: "persisted.gguf", Size: 100, State: downloads.StateQueued}}); err != nil {
		t.Fatal(err)
	}
	if err := downloadStore.SetFileDownloading(ctx, persistedJob.ID, "persisted.gguf", 37, "etag-persisted"); err != nil {
		t.Fatal(err)
	}
	if err := downloadStore.RefreshAggregate(ctx, persistedJob.ID, 456); err != nil {
		t.Fatal(err)
	}

	// Model import: seed an already-completed provider download through its
	// store API so Prepare does not launch network traffic. Then force the
	// persisted import back to DOWNLOADING to exercise restart reconciliation.
	artifact := huggingface.Artifact{
		ID: "qualification-import-artifact", Name: "demo-Q4_K_M.gguf", Quantization: "Q4_K_M",
		ModelBytes: 4, TotalBytes: 4, ShardCount: 1, ExpectedShards: 1, Complete: true,
		Files: []huggingface.File{{Path: "demo-Q4_K_M.gguf", Size: 4, OID: "qualification-oid"}},
	}
	detail := huggingface.ModelDetail{ID: "acme/import", Revision: "qualification-revision"}
	importJob := downloads.Job{
		ID: "qualification-import-download", Provider: "huggingface", RepoID: detail.ID,
		Revision: detail.Revision, ArtifactID: artifact.ID, Name: artifact.Name,
		Quantization: artifact.Quantization, State: downloads.StateQueued, TotalBytes: artifact.TotalBytes,
	}
	if err := downloadStore.Create(ctx, importJob, []downloads.File{{Path: artifact.Files[0].Path, Size: 4, OID: artifact.Files[0].OID, State: downloads.StateQueued}}); err != nil {
		t.Fatal(err)
	}
	relPath := filepath.ToSlash(filepath.Join("huggingface", "acme", "import", artifact.Files[0].Path))
	absPath := filepath.Join(modelsDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := downloadStore.CompleteFile(ctx, importJob.ID, artifact.Files[0].Path, 4, relPath); err != nil {
		t.Fatal(err)
	}
	if err := downloadStore.CompleteJob(ctx, importJob.ID); err != nil {
		t.Fatal(err)
	}
	modelService := models.New(store, modelsDir)
	downloadManager := downloads.New(ctx, store, modelsDir, nil)
	starter := &qualificationStarter{}
	importService := modelimports.New(store, modelsDir, modelService, downloadManager, starter)
	prepared, err := importService.Prepare(ctx, detail, artifact, modelimports.PrepareInput{
		Name: "Qualification Model", ContextLength: 4096,
		FirstInstance: modelimports.FirstInstanceInput{
			Name: "Qualification Instance", Slug: "qualification-instance",
			AutoloadEnabled: true, EvictionEnabled: true, Start: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Download.ID != importJob.ID || prepared.Model.ID == "" || prepared.Instance.ID == "" {
		t.Fatalf("prepared import=%+v", prepared)
	}
	statuses, err := importService.List(ctx)
	if err != nil || len(statuses) != 1 || statuses[0].State != modelimports.StateCompleted {
		t.Fatalf("initial import statuses=%+v err=%v", statuses, err)
	}
	if !starter.called(prepared.Instance.ID) {
		t.Fatalf("prepare did not perform initial start: %v", starter.calls)
	}
	initialStarts := len(starter.calls)
	if _, err := store.ExecContext(ctx, `UPDATE provider_imports SET state=?,start_attempted=0 WHERE id=?`, modelimports.StateDownloading, statuses[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := importService.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	statuses, err = importService.List(ctx)
	if err != nil || len(statuses) != 1 || statuses[0].State != modelimports.StateCompleted || len(starter.calls) != initialStarts+1 || starter.calls[len(starter.calls)-1] != prepared.Instance.ID {
		t.Fatalf("reconciled import statuses=%+v starter=%v err=%v", statuses, starter.calls, err)
	}

	// Observability: stage llama.cpp timing before finalization, promote it on
	// finalization, persist request-log context, then exercise buffered writeback.
	obs := observability.New(store)
	now := time.Now().UnixMilli()
	promptN := int64(11)
	finishReason := "stop"
	if err := obs.StageInferenceTurnStats(ctx, "qualification-request-a", observability.InferenceTurnStats{PromptN: &promptN, FinishReason: &finishReason}); err != nil {
		t.Fatal(err)
	}
	begin := observability.RequestRecord{StartedAt: now, InstanceID: prepared.Instance.ID, Endpoint: "/v1/chat/completions"}
	if err := obs.BeginCorrelatedRequest(ctx, "qualification-request-a", begin); err != nil {
		t.Fatal(err)
	}
	if err := obs.UpdateRequestLogContext(ctx, "qualification-request-a", "qualification-session", prepared.Instance.ID); err != nil {
		t.Fatal(err)
	}
	final := begin
	final.FinishedAt = now + 10
	final.StatusCode = 200
	final.Result = "success"
	final.PromptTokens = 11
	final.GeneratedTokens = 7
	final.TotalTokens = 18
	if err := obs.FinalizeCorrelatedRequest(ctx, "qualification-request-a", nil, final); err != nil {
		t.Fatal(err)
	}
	var promotedPrompt int64
	var promotedFinish string
	if err := store.QueryRowContext(ctx, `SELECT prompt_n,finish_reason FROM inference_request_timings WHERE request_id=?`, "qualification-request-a").Scan(&promotedPrompt, &promotedFinish); err != nil || promotedPrompt != 11 || promotedFinish != "stop" {
		t.Fatalf("promoted timing prompt=%d finish=%q err=%v", promotedPrompt, promotedFinish, err)
	}
	logEntry, err := obs.GetRequestLogByRequestID(ctx, "qualification-request-a")
	if err != nil || logEntry.SessionID != "qualification-session" || logEntry.InstanceID != prepared.Instance.ID {
		t.Fatalf("request log=%+v err=%v", logEntry, err)
	}

	writebackCtx, cancelWriteback := context.WithCancel(context.Background())
	obs.StartWriteback(writebackCtx)
	wb := observability.RequestRecord{StartedAt: now + 100, InstanceID: prepared.Instance.ID, Endpoint: "/v1/responses"}
	if err := obs.BeginCorrelatedRequest(ctx, "qualification-request-b", wb); err != nil {
		cancelWriteback()
		t.Fatal(err)
	}
	if err := obs.AttachRequestLogContext(ctx, "qualification-request-b", "qualification-session", prepared.Instance.ID); err != nil {
		cancelWriteback()
		t.Fatal(err)
	}
	wb.FinishedAt = now + 120
	wb.StatusCode = 201
	wb.Result = "success"
	if err := obs.FinalizeCorrelatedRequest(ctx, "qualification-request-b", nil, wb); err != nil {
		cancelWriteback()
		t.Fatal(err)
	}
	if err := obs.Flush(ctx); err != nil {
		cancelWriteback()
		t.Fatal(err)
	}
	cancelWriteback()
	logs, err := obs.ListRequestLogs(ctx, observability.RequestFilters{Limit: 1}, "")
	if err != nil || len(logs) != 1 || logs[0].RequestID != "qualification-request-b" {
		t.Fatalf("request log page 1=%+v err=%v", logs, err)
	}
	logs, err = obs.ListRequestLogs(ctx, observability.RequestFilters{Limit: 1, Offset: 1}, "")
	if err != nil || len(logs) != 1 || logs[0].RequestID != "qualification-request-a" {
		t.Fatalf("request log page 2=%+v err=%v", logs, err)
	}

	// Benchmark run/results persistence stays entirely on the benchmark store.
	benchmarkStore := benchmark.NewSQLStore(store)
	config := benchmark.InstanceConfigSnapshot{SchemaVersion: benchmark.ConfigSchemaVersion, GPUMode: "auto", Options: map[string]string{}}
	run := benchmark.Run{
		ID: "qualification-benchmark", InstanceID: prepared.Instance.ID,
		InstanceSlugSnapshot: prepared.Instance.Slug, InstanceNameSnapshot: prepared.Instance.Name,
		InstanceConfig: config, EffectiveConfig: config,
		ModelID: prepared.Model.ID, ModelSlugSnapshot: prepared.Model.Slug, ModelNameSnapshot: prepared.Model.Name,
		Artifact: benchmark.ArtifactSnapshot{Path: relPath, Size: 4, Files: []benchmark.ArtifactFileSnapshot{{Path: relPath, Size: 4}}},
		Workload: benchmark.WorkloadProfile{ID: benchmark.DefaultWorkloadID, Version: benchmark.WorkloadSchemaVersion, PromptTokens: []int{32}, GenerationTokens: []int{8}, Repetitions: 1},
		ResolvedArgv: []string{"llama-bench", "-m", absPath}, Status: benchmark.StatusQueued,
		CreatedAt: time.Unix(1700000000, 0).UTC(), BenchmarkSchemaVersion: benchmark.BenchmarkSchemaVersion, ParserSchemaVersion: benchmark.ParserSchemaVersion,
	}
	if err := benchmarkStore.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	started := time.Unix(1700000001, 0).UTC()
	if _, err := benchmarkStore.TransitionRun(ctx, run.ID, benchmark.StatusQueued, benchmark.StatusRunning, benchmark.TransitionUpdate{StartedAt: &started}); err != nil {
		t.Fatal(err)
	}
	completed := time.Unix(1700000002, 0).UTC()
	benchResult := benchmark.Result{CaseID: "pp32-tg8", PromptTokens: 32, GenerationTokens: 8, Repetitions: 1, AverageNS: 1234, AverageTokensPS: 42.5, RawFields: json.RawMessage(`{"n_depth":4096}`)}
	completedRun, err := benchmarkStore.CompleteRun(ctx, run.ID, benchmark.Completion{CompletedAt: completed, DiagnosticOutput: "qualification"}, []benchmark.Result{benchResult})
	if err != nil || completedRun.Status != benchmark.StatusCompleted || len(completedRun.Results) != 1 {
		t.Fatalf("completed benchmark=%+v err=%v", completedRun, err)
	}

	// Runtime ownership metadata is also checked across the reopen below.
	runtimeStore := supervisor.NewSQLStore(store)
	runtimeRecord := supervisor.WorkerRecord{InstanceID: prepared.Instance.ID, Generation: "qualification-runtime", PID: 4321, StartTicks: 8765, Port: 12001}
	if err := runtimeStore.Upsert(ctx, runtimeRecord); err != nil {
		t.Fatal(err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open()
	defer store.Close()

	downloadStore = downloads.NewDownloadStore(store)
	persisted, err := downloadStore.Get(ctx, persistedJob.ID)
	persistedFiles, filesErr := downloadStore.Files(ctx, persistedJob.ID)
	if err != nil || filesErr != nil || persisted.DownloadedBytes != 37 || persisted.SpeedBPS != 456 || len(persistedFiles) != 1 || persistedFiles[0].DownloadedBytes != 37 {
		t.Fatalf("reopened download=%+v files=%+v err=%v filesErr=%v", persisted, persistedFiles, err, filesErr)
	}
	modelService = models.New(store, modelsDir)
	importService = modelimports.New(store, modelsDir, modelService, downloads.New(ctx, store, modelsDir, nil), nil)
	statuses, err = importService.List(ctx)
	if err != nil || len(statuses) != 1 || statuses[0].State != modelimports.StateCompleted || statuses[0].InstanceID != prepared.Instance.ID {
		t.Fatalf("reopened import statuses=%+v err=%v", statuses, err)
	}
	obs = observability.New(store)
	if got, err := obs.GetRequestLogByRequestID(ctx, "qualification-request-b"); err != nil || got.RequestID != "qualification-request-b" {
		t.Fatalf("reopened request log=%+v err=%v", got, err)
	}
	benchmarkStore = benchmark.NewSQLStore(store)
	if got, err := benchmarkStore.GetRun(ctx, run.ID); err != nil || got.Status != benchmark.StatusCompleted || len(got.Results) != 1 || got.Results[0].CaseID != "pp32-tg8" {
		t.Fatalf("reopened benchmark=%+v err=%v", got, err)
	}
	runtimeStore = supervisor.NewSQLStore(store)
	if got, err := runtimeStore.Get(ctx, prepared.Instance.ID); err != nil || got != runtimeRecord {
		t.Fatalf("reopened runtime=%+v err=%v", got, err)
	}
}

func TestPostgresAPIKeyConcurrentRevocationVisibility(t *testing.T) {
	base := os.Getenv("LLAMARACK_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("LLAMARACK_TEST_POSTGRES_URL is not configured")
	}
	ctx := context.Background()
	store, err := database.OpenConfigured(ctx, "", isolatedPostgresDSN(t, base))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := auth.New(store, time.Hour)
	user, err := service.Bootstrap(ctx, "qualification-admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	key, secret, err := service.CreateAPIKeyForUser(ctx, "qualification-key", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AuthenticateAPIKey(ctx, secret); err != nil {
		t.Fatal(err)
	}
	if err := service.SetAPIKeyEnabled(ctx, key.ID, false); err != nil {
		t.Fatal(err)
	}

	const readers = 16
	errs := make(chan error, readers)
	start := make(chan struct{})
	for range readers {
		go func() {
			<-start
			errs <- service.AuthenticateAPIKey(ctx, secret)
		}()
	}
	close(start)
	for range readers {
		if err := <-errs; !errors.Is(err, auth.ErrAPIKeyInvalid) {
			t.Fatalf("concurrent authentication after revocation=%v", err)
		}
	}
}
