package benchmark

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestSQLStoreHydratesContextDepthFromRawResult(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewSQLStore(db)
	created := time.Unix(1_780_000_100, 0).UTC()
	run := testRun(created)
	run.ID = "depth-run"
	run.Workload.Version = WorkloadSchemaVersion
	run.Workload.ContextDepths = []int{2048}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	started := created.Add(time.Second)
	if _, err := store.TransitionRun(ctx, run.ID, StatusQueued, StatusRunning, TransitionUpdate{StartedAt: &started}); err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteRun(ctx, run.ID, Completion{CompletedAt: started.Add(time.Second)}, []Result{{
		CaseID: "tg-128-d2048", GenerationTokens: 128, ContextDepth: 2048, Repetitions: 2,
		AverageTokensPS: 42, RawFields: []byte(`{"n_gen":128,"n_depth":2048,"avg_ts":42}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Results) != 1 || completed.Results[0].ContextDepth != 2048 {
		t.Fatalf("completed results=%+v", completed.Results)
	}
	loaded, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Results) != 1 || loaded.Results[0].ContextDepth != 2048 || loaded.Results[0].CaseID != "tg-128-d2048" {
		t.Fatalf("loaded results=%+v", loaded.Results)
	}
}
