package llamaconfig

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
)

func TestReplaceGlobalSkipsBlankKeysAndRejectsNormalizedDuplicates(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if err := store.ReplaceGlobal(ctx, map[string]string{"---": "ignored", "threads": "4"}); err != nil {
		t.Fatal(err)
	}
	global, err := store.Global(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(global) != 1 || global["threads"] != "4" {
		t.Fatalf("global=%+v", global)
	}
	if err := store.ReplaceGlobal(ctx, map[string]string{"--threads": "6", "threads": "4"}); err == nil {
		t.Fatal("expected normalized duplicate option error")
	}
}

func TestLaunchOptionsEmptyProfileAndMissingInstance(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if err := store.ReplaceGlobal(ctx, map[string]string{"threads": "4"}); err != nil {
		t.Fatal(err)
	}
	launch, effective, err := store.LaunchOptions(ctx, llamacpp.Profile{}, "m1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(launch) != 0 || effective.Values["threads"] != "4" {
		t.Fatalf("launch=%+v effective=%+v", launch, effective)
	}
	if _, err := store.Effective(ctx, "", "missing-instance"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing instance err=%v", err)
	}
}

func TestReadOptionsScanError(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if _, err := readOptions(ctx, store.db, `SELECT 1`); err == nil {
		t.Fatal("expected scan error")
	}
}

func TestStoreClosedDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if err := store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Global(ctx); err == nil {
		t.Fatal("expected Global error")
	}
	if err := store.ReplaceGlobal(ctx, map[string]string{"threads": "4"}); err == nil {
		t.Fatal("expected ReplaceGlobal error")
	}
	if _, err := store.Effective(ctx, "m1", ""); err == nil {
		t.Fatal("expected Effective error")
	}
	if _, _, err := store.LaunchOptions(ctx, llamacpp.Profile{}, "m1", ""); err == nil {
		t.Fatal("expected LaunchOptions error")
	}
}

func TestEffectiveModelAndInstanceOptionQueryErrors(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	if _, err := store.db.ExecContext(ctx, `DROP TABLE model_options`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Effective(ctx, "m1", ""); err == nil {
		t.Fatal("expected model option query error")
	}

	store = testStore(t)
	if _, err := store.db.ExecContext(ctx, `DROP TABLE instance_options`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Effective(ctx, "m1", "i1"); err == nil {
		t.Fatal("expected instance option query error")
	}
}
