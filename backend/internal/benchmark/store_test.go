package benchmark

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
	"github.com/brantje/llamarack/backend/internal/hardware"
)

func TestSQLStoreLifecycleAndImmutableCompletion(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewSQLStore(db)
	created := time.Unix(1_780_000_000, 0).UTC()
	run := testRun(created)
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceSlugSnapshot != "coder" || got.Artifact.Fingerprint != "sha256:artifact" || got.Hardware.Observed.GPUs[0].Name != "RTX Test" {
		t.Fatalf("stored run lost immutable context: %+v", got)
	}

	page, err := store.ListRuns(ctx, Filter{InstanceID: run.InstanceID, Status: StatusQueued, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Limit != 100 {
		t.Fatalf("unexpected page %+v", page)
	}

	started := created.Add(time.Second)
	got, err = store.TransitionRun(ctx, run.ID, StatusQueued, StatusRunning, TransitionUpdate{StartedAt: &started})
	if err != nil || got.Status != StatusRunning || got.StartedAt == nil || !got.StartedAt.Equal(started) {
		t.Fatalf("running transition run=%+v err=%v", got, err)
	}
	completed := started.Add(2 * time.Second)
	got, err = store.CompleteRun(ctx, run.ID, Completion{CompletedAt: completed, DiagnosticOutput: "ok"}, []Result{
		{CaseID: "pp-512", PromptTokens: 512, Repetitions: 5, AverageTokensPS: 123.4, StdDevTokensPS: 1.2, RawFields: []byte(`{"future_field":"kept"}`)},
		{CaseID: "tg-128", GenerationTokens: 128, Repetitions: 5, AverageTokensPS: 45.6, RawFields: []byte(`{"n_gen":128}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted || got.CompletedAt == nil || len(got.Results) != 2 || string(got.Results[0].RawFields) != `{"future_field":"kept"}` {
		t.Fatalf("completed run=%+v", got)
	}
	if _, err := store.TransitionRun(ctx, run.ID, StatusCompleted, StatusFailed, TransitionUpdate{Failure: "must not mutate"}); !errors.Is(err, ErrTransitionConflict) {
		t.Fatalf("completed run mutated, err=%v", err)
	}
	if _, err := store.CompleteRun(ctx, run.ID, Completion{CompletedAt: completed}, nil); !errors.Is(err, ErrTransitionConflict) {
		t.Fatalf("completed run completed twice, err=%v", err)
	}

	if err := store.DeleteRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRun(ctx, run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted run lookup err=%v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM benchmark_results WHERE run_id=?`, run.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("result cascade count=%d err=%v", count, err)
	}
}

func TestSQLStoreTransitionConflictsAndFilters(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewSQLStore(db)
	base := time.Unix(1_780_000_000, 0).UTC()
	one := testRun(base)
	two := testRun(base.Add(time.Second))
	two.ID = "run-2"
	two.InstanceID = "instance-2"
	two.ModelID = "model-2"
	if err := store.CreateRun(ctx, one); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRun(ctx, two); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionRun(ctx, one.ID, StatusRunning, StatusFailed, TransitionUpdate{Failure: "wrong source"}); !errors.Is(err, ErrTransitionConflict) {
		t.Fatalf("wrong source transition err=%v", err)
	}
	if _, err := store.TransitionRun(ctx, "missing", StatusQueued, StatusRunning, TransitionUpdate{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing transition err=%v", err)
	}
	page, err := store.ListRuns(ctx, Filter{ModelID: "model-2", Limit: 10, Offset: -1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != two.ID || page.Offset != 0 {
		t.Fatalf("filtered page=%+v err=%v", page, err)
	}
}

func testRun(created time.Time) Run {
	return Run{
		ID:                     "run-1",
		InstanceID:             "deleted-instance-id",
		InstanceSlugSnapshot:   "coder",
		InstanceNameSnapshot:   "Coder",
		InstanceConfig:         InstanceConfigSnapshot{SchemaVersion: ConfigSchemaVersion, GPUMode: "manual", GPUDevices: []string{"CUDA0"}, Options: map[string]string{"ctx-size": "4096"}},
		ModelID:                "deleted-model-id",
		ModelSlugSnapshot:      "qwen",
		ModelNameSnapshot:      "Qwen",
		Artifact:               ArtifactSnapshot{Path: "qwen.gguf", Fingerprint: "sha256:artifact", Size: 10, Quantization: "Q4_K_M", Architecture: "qwen2", ShardCount: 1, ExpectedShards: 1, Files: []ArtifactFileSnapshot{{Path: "qwen.gguf", Size: 10, SHA256: "abc"}}},
		Workload:               WorkloadProfile{ID: DefaultWorkloadID, Version: WorkloadSchemaVersion, PromptTokens: []int{512}, GenerationTokens: []int{128}, Repetitions: 5, Warmup: true},
		ResolvedArgv:           []string{"--model", "/models/qwen.gguf", "--output", "json"},
		Status:                 StatusQueued,
		CreatedAt:              created,
		Build:                  BuildSnapshot{LlamaRackVersion: "1.1.0", LlamaRackCommit: "abc", LlamaBenchVersion: "b7000"},
		Hardware:               HardwareSnapshot{Observed: hardware.Snapshot{GPUs: []hardware.GPU{{ID: "CUDA0", Name: "RTX Test", TotalBytes: 16 << 30}}}, CPU: CPUSnapshot{Model: "Test CPU", LogicalThreads: 8, Architecture: "amd64", OS: "linux"}},
		BenchmarkSchemaVersion: BenchmarkSchemaVersion,
		ParserSchemaVersion:    ParserSchemaVersion,
	}
}
