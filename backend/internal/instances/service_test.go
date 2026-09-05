package instances

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func testService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO models(id,name,gguf_path,total_bytes) VALUES('m1','Model','model.gguf',42)`); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func boolp(v bool) *bool { return &v }

func intp(v int) *int { return &v }

func TestSlugifyAndValidation(t *testing.T) {
	if got := Slugify("  My Instance / GPU 0  "); got != "my-instance-gpu-0" {
		t.Fatalf("slug=%q", got)
	}
	if got := Slugify("Ä Model"); got != "ä-model" {
		t.Fatalf("unicode slug=%q", got)
	}
	for _, in := range []CreateInput{
		{ModelID: "m1"},
		{ModelID: "m1", Name: "---"},
		{ModelID: "m1", Name: "One", Slug: "---"},
		{Name: "One"},
		{ModelID: "m1", Name: "One", Priority: "urgent"},
		{ModelID: "m1", Name: "One", GPUMode: "magic"},
		{ModelID: "m1", Name: "One", IdleUnloadSeconds: -1},
		{ModelID: "m1", Name: "One", MaxPendingRequests: intp(-1)},
	} {
		if _, err := normalizeCreate(in); err == nil {
			t.Fatalf("expected validation error for %+v", in)
		}
	}
}

func TestCreateListGetOptionsUpdateRenameDuplicateDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := testService(t)
	i, err := s.Create(ctx, CreateInput{
		ModelID: "m1", Name: "Coder Primary", Slug: "Coding API", Enabled: boolp(false), Autoload: boolp(false), AlwaysOn: true,
		Priority: "high", EvictionEnabled: boolp(false), IdleUnloadSeconds: 90, MaxPendingRequests: intp(8),
		GPUMode: "manual", GPUDevices: []string{"0", " 1 ", "0", ""}, TensorSplit: "1,1",
		Options: map[string]string{"ctx-size": "8192", " threads ": "8", "": "ignored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if i.ID == "" || i.ID == i.Slug || i.Slug != "coding-api" || i.Enabled || i.Autoload || !i.AlwaysOn || i.Priority != "high" || i.EvictionEnabled || i.MaxPendingRequests != 8 || len(i.GPUDevices) != 2 {
		t.Fatalf("created=%+v", i)
	}
	got, err := s.GetByID(ctx, i.ID)
	if err != nil || got.TensorSplit != "1,1" || len(got.GPUDevices) != 2 {
		t.Fatalf("get by id=%+v err=%v", got, err)
	}
	bySlug, err := s.GetBySlug(ctx, i.Slug)
	if err != nil || bySlug.ID != i.ID {
		t.Fatalf("get by slug=%+v err=%v", bySlug, err)
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list=%+v err=%v", items, err)
	}
	byModel, err := s.ListByModel(ctx, "m1")
	if err != nil || len(byModel) != 1 {
		t.Fatalf("byModel=%+v err=%v", byModel, err)
	}
	opts, err := s.Options(ctx, i.ID)
	if err != nil || len(opts) != 2 || opts["threads"] != "8" {
		t.Fatalf("options=%+v err=%v", opts, err)
	}

	updated, err := s.Update(ctx, i.ID, UpdateInput{Name: "Coder Renamed", Slug: "Renamed API", Enabled: boolp(true), Autoload: boolp(true), Priority: "normal", EvictionEnabled: boolp(true), GPUMode: "auto", Options: map[string]string{"flash-attn": "true"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != i.ID || updated.Slug != "renamed-api" || updated.ModelID != "m1" || !updated.Enabled || !updated.Autoload {
		t.Fatalf("updated=%+v", updated)
	}
	if updated.MaxPendingRequests != 8 {
		t.Fatalf("omitted pending limit should keep override=%+v", updated)
	}
	if _, err := s.GetBySlug(ctx, "coding-api"); err == nil {
		t.Fatal("old slug should no longer resolve")
	}
	if stable, err := s.GetByID(ctx, i.ID); err != nil || stable.Slug != "renamed-api" {
		t.Fatalf("durable id should still resolve after slug change: %+v err=%v", stable, err)
	}
	opts, _ = s.Options(ctx, updated.ID)
	if len(opts) != 1 || opts["flash-attn"] != "true" {
		t.Fatalf("renamed options=%+v", opts)
	}

	copy, err := s.Duplicate(ctx, updated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copy.ID == updated.ID || copy.Slug != "coder-renamed-copy" || copy.ModelID != updated.ModelID {
		t.Fatalf("copy=%+v", copy)
	}
	copy2, err := s.Duplicate(ctx, updated.ID)
	if err != nil || copy2.ID == copy.ID || copy2.Slug != "coder-renamed-copy-2" {
		t.Fatalf("copy2=%+v err=%v", copy2, err)
	}

	if err := s.Delete(ctx, updated.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, updated.ID); err == nil {
		t.Fatal("second delete should fail")
	}
}

func TestCreateAndUpdateErrors(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	if _, err := s.Create(ctx, CreateInput{ModelID: "missing", Name: "Nope"}); err == nil {
		t.Fatal("missing model should fail")
	}
	if _, err := s.Update(ctx, "missing", UpdateInput{Name: "Nope", ModelID: "m1"}); err == nil {
		t.Fatal("missing instance update should fail")
	}
	created, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "One"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "Different Name", Slug: "One"}); err == nil {
		t.Fatal("duplicate slug should fail")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx); err == nil {
		t.Fatal("closed db list should fail")
	}
	if _, err := s.Options(ctx, created.ID); err == nil {
		t.Fatal("closed db options should fail")
	}
	if err := s.Delete(ctx, created.ID); err == nil {
		t.Fatal("closed db delete should fail")
	}
}
