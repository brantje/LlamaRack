package supervisor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func TestEnsureInstallationIDIsStable(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, err := EnsureInstallationID(ctx, db)
	if err != nil || first == "" {
		t.Fatalf("first installation id=%q err=%v", first, err)
	}
	second, err := EnsureInstallationID(ctx, db)
	if err != nil || second != first {
		t.Fatalf("second installation id=%q want=%q err=%v", second, first, err)
	}
}

func TestSQLStoreUpsertGetListDelete(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewSQLStore(db)

	if _, err := store.Get(ctx, "missing"); err != ErrRuntimeNotFound {
		t.Fatalf("missing get err=%v", err)
	}

	rec := WorkerRecord{InstanceID: "coding", Generation: "gen-1", PID: 42, StartTicks: 99, Port: 10001}
	if err := store.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "coding")
	if err != nil || got != rec {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	rec.Generation = "gen-2"
	rec.PID = 43
	if err := store.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, "coding")
	if err != nil || got.Generation != "gen-2" || got.PID != 43 {
		t.Fatalf("updated get=%+v err=%v", got, err)
	}
	listed, err := store.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].InstanceID != "coding" {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	if err := store.Delete(ctx, "coding"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "coding"); err != ErrRuntimeNotFound {
		t.Fatalf("deleted get err=%v", err)
	}
}

func TestMemoryStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	rec := WorkerRecord{InstanceID: "one", Generation: "g", PID: 7, StartTicks: 8, Port: 9}
	if err := store.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "one")
	if err != nil || got != rec {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	if err := store.Delete(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "one"); err != ErrRuntimeNotFound {
		t.Fatalf("deleted err=%v", err)
	}
}
