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
	db.SetMaxOpenConns(1)
	listCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	page, err = store.ListRuns(listCtx, Filter{Status: StatusCompleted})
	if err != nil || len(page.Items) != 1 || len(page.Items[0].Results) != 2 {
		t.Fatalf("history measurements missing: page=%+v err=%v", page, err)
	}
	if page.Items[0].Results[0].AverageTokensPS != 123.4 || page.Items[0].Results[1].AverageTokensPS != 45.6 || page.Items[0].Results[1].GenerationTokens != 128 {
		t.Fatalf("history prompt/generation measurements changed: %+v", page.Items[0].Results)
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

func TestSQLStoreValidationRollbackAndStorageErrors(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewSQLStore(db)
	for _, run := range []Run{{}, {ID: "id", Status: "INVALID"}} {
		if err := store.CreateRun(ctx, run); err == nil {
			t.Fatalf("accepted invalid run %+v", run)
		}
	}
	run := testRun(time.Time{})
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil || got.CreatedAt.IsZero() {
		t.Fatalf("run=%+v err=%v", got, err)
	}
	if _, err := store.TransitionRun(ctx, run.ID, "INVALID", StatusRunning, TransitionUpdate{}); err == nil {
		t.Fatal("accepted invalid status")
	}
	if _, err := store.TransitionRun(ctx, run.ID, StatusQueued, StatusRunning, TransitionUpdate{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteRun(ctx, run.ID, Completion{}, []Result{{RawFields: []byte("bad")}}); err == nil {
		t.Fatal("accepted invalid raw JSON")
	}
	got, _ = store.GetRun(ctx, run.ID)
	if got.Status != StatusRunning || len(got.Results) != 0 {
		t.Fatalf("partial completion persisted: %+v", got)
	}
	got, err = store.CompleteRun(ctx, run.ID, Completion{}, []Result{{CaseID: "pp-1", PromptTokens: 1, AverageNS: 20, StdDevNS: 2}})
	if err != nil || len(got.Results) != 1 || got.Results[0].AverageNS != 20 || got.Results[0].StdDevNS != 2 || string(got.Results[0].RawFields) != "{}" {
		t.Fatalf("completion=%+v err=%v", got, err)
	}
	if err := store.DeleteRun(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v", err)
	}
	// Broken persisted JSON must be reported, never silently treated as a valid run.
	if _, err := db.ExecContext(ctx, `UPDATE benchmark_runs SET instance_config_snapshot='invalid' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRun(ctx, run.ID); err == nil {
		t.Fatal("corrupt config was accepted")
	}
	if _, err := store.ListRuns(ctx, Filter{}); err == nil {
		t.Fatal("corrupt history was accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { return store.CreateRun(ctx, testRun(time.Now())) },
		func() error { _, err := store.GetRun(ctx, "id"); return err },
		func() error { _, err := store.ListRuns(ctx, Filter{}); return err },
		func() error {
			_, err := store.TransitionRun(ctx, "id", StatusQueued, StatusRunning, TransitionUpdate{})
			return err
		},
		func() error { _, err := store.CompleteRun(ctx, "id", Completion{}, nil); return err },
		func() error { return store.DeleteRun(ctx, "id") },
	} {
		if err := call(); err == nil {
			t.Fatal("closed store error ignored")
		}
	}
	for _, absent := range []*SQLStore{nil, NewSQLStore(nil)} {
		if err := absent.CreateRun(ctx, run); err == nil {
			t.Fatal("nil store create")
		}
		if _, err := absent.GetRun(ctx, run.ID); err == nil {
			t.Fatal("nil store get")
		}
		if _, err := absent.ListRuns(ctx, Filter{}); err == nil {
			t.Fatal("nil store list")
		}
		if err := absent.DeleteRun(ctx, run.ID); err == nil {
			t.Fatal("nil store delete")
		}
	}
}
