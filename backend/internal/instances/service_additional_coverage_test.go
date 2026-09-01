package instances

import (
	"context"
	"testing"
)

func TestUpdateValidationConflictAndNilOptionsBranches(t *testing.T) {
	ctx := context.Background()
	s, _ := testService(t)
	first, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "First"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Update(ctx, first.ID, UpdateInput{Name: "First", Priority: "urgent"}); err == nil {
		t.Fatal("expected invalid update priority")
	}
	if _, err := s.Update(ctx, first.ID, UpdateInput{Name: "First", GPUMode: "magic"}); err == nil {
		t.Fatal("expected invalid update GPU mode")
	}
	if _, err := s.Update(ctx, first.ID, UpdateInput{Name: "First", IdleUnloadSeconds: -1}); err == nil {
		t.Fatal("expected invalid update idle timeout")
	}
	if _, err := s.Update(ctx, first.ID, UpdateInput{Name: "First", MaxPendingRequests: -1}); err == nil {
		t.Fatal("expected invalid update pending limit")
	}
	if _, err := s.Update(ctx, first.ID, UpdateInput{Name: "First", Slug: second.ID}); err == nil {
		t.Fatal("expected unique slug conflict")
	}

	updated, err := s.Update(ctx, first.ID, UpdateInput{
		ModelID: "m1", Name: "First Defaulted", Slug: "first-defaulted",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != "first-defaulted" || !updated.Enabled || !updated.Autoload || !updated.EvictionEnabled || updated.Priority != "normal" || updated.GPUMode != "auto" {
		t.Fatalf("unexpected updated defaults: %+v", updated)
	}
}

func TestCreateAndDuplicateOptionStorageErrors(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	base, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "Base", Options: map[string]string{"ctx-size": "4096"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE instance_options`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Duplicate(ctx, base.ID); err == nil {
		t.Fatal("expected duplicate options read error")
	}
	if _, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "No Options Table", Options: map[string]string{"threads": "4"}}); err == nil {
		t.Fatal("expected create options write error")
	}
}

func TestListScanAndClosedDatabaseBranches(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	created, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "Malformed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE instances SET enabled='not-an-integer' WHERE id=?`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx); err == nil {
		t.Fatal("expected list scan error")
	}
	if _, err := s.ListByModel(ctx, "m1"); err == nil {
		t.Fatal("expected list-by-model scan error")
	}

	s, db = testService(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "Closed"}); err == nil {
		t.Fatal("expected create closed-db error")
	}
	if _, err := s.Get(ctx, "missing"); err == nil {
		t.Fatal("expected get closed-db error")
	}
	if _, err := s.Update(ctx, "missing", UpdateInput{Name: "Closed"}); err == nil {
		t.Fatal("expected update closed-db error")
	}
	if _, err := s.Duplicate(ctx, "missing"); err == nil {
		t.Fatal("expected duplicate closed-db error")
	}
	if _, err := s.ListByModel(ctx, "m1"); err == nil {
		t.Fatal("expected list-by-model closed-db error")
	}
}

func TestSmallInstanceHelpers(t *testing.T) {
	if got := itoa(0); got != "0" {
		t.Fatalf("itoa(0)=%q", got)
	}
	if joinDevices(nil) != nil {
		t.Fatal("empty devices should persist as NULL")
	}
	if nullString("   ") != nil {
		t.Fatal("blank tensor split should persist as NULL")
	}
}
