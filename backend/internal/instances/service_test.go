package instances

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/database"
)

func testService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`INSERT INTO models(id,name,gguf_path,total_bytes) VALUES('m1','Model','model.gguf',42)`); err != nil { t.Fatal(err) }
	return New(db), db
}

func boolp(v bool) *bool { return &v }

func TestSlugifyAndValidation(t *testing.T) {
	if got := Slugify("  My Instance / GPU 0  "); got != "my-instance-gpu-0" { t.Fatalf("slug=%q", got) }
	if got := Slugify("Ä Model"); got != "ä-model" { t.Fatalf("unicode slug=%q", got) }
	for _, in := range []CreateInput{
		{ModelID: "m1"},
		{ModelID: "m1", Name: "---"},
		{Name: "One"},
		{ModelID: "m1", Name: "One", Priority: "urgent"},
		{ModelID: "m1", Name: "One", GPUMode: "magic"},
		{ModelID: "m1", Name: "One", IdleUnloadSeconds: -1},
	} {
		if _, err := normalize(in); err == nil { t.Fatalf("expected validation error for %+v", in) }
	}
}

func TestCreateListGetOptionsUpdateRenameDuplicateDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := testService(t)
	i, err := s.Create(ctx, CreateInput{
		ModelID: "m1", Name: "Coder Primary", Enabled: boolp(false), Autoload: boolp(false), AlwaysOn: true,
		Priority: "high", EvictionEnabled: boolp(false), IdleUnloadSeconds: 90,
		GPUMode: "manual", GPUDevices: []string{"0", " 1 ", "0", ""}, TensorSplit: "1,1",
		Options: map[string]string{"ctx-size": "8192", " threads ": "8", "": "ignored"},
	})
	if err != nil { t.Fatal(err) }
	if i.ID != "coder-primary" || i.Enabled || i.Autoload || !i.AlwaysOn || i.Priority != "high" || i.EvictionEnabled || len(i.GPUDevices) != 2 { t.Fatalf("created=%+v", i) }
	got, err := s.Get(ctx, i.ID)
	if err != nil || got.TensorSplit != "1,1" || len(got.GPUDevices) != 2 { t.Fatalf("get=%+v err=%v", got, err) }
	items, err := s.List(ctx)
	if err != nil || len(items) != 1 { t.Fatalf("list=%+v err=%v", items, err) }
	byModel, err := s.ListByModel(ctx, "m1")
	if err != nil || len(byModel) != 1 { t.Fatalf("byModel=%+v err=%v", byModel, err) }
	opts, err := s.Options(ctx, i.ID)
	if err != nil || len(opts) != 2 || opts["threads"] != "8" { t.Fatalf("options=%+v err=%v", opts, err) }

	updated, err := s.Update(ctx, i.ID, UpdateInput{Name: "Coder Renamed", Enabled: boolp(true), Autoload: boolp(true), Priority: "normal", EvictionEnabled: boolp(true), GPUMode: "auto", Options: map[string]string{"flash-attn": "true"}})
	if err != nil { t.Fatal(err) }
	if updated.ID != "coder-renamed" || updated.ModelID != "m1" || !updated.Enabled || !updated.Autoload { t.Fatalf("updated=%+v", updated) }
	if _, err := s.Get(ctx, i.ID); err == nil { t.Fatal("old id should no longer resolve") }
	opts, _ = s.Options(ctx, updated.ID)
	if len(opts) != 1 || opts["flash-attn"] != "true" { t.Fatalf("renamed options=%+v", opts) }

	copy, err := s.Duplicate(ctx, updated.ID)
	if err != nil { t.Fatal(err) }
	if copy.ID != "coder-renamed-copy" || copy.ModelID != updated.ModelID { t.Fatalf("copy=%+v", copy) }
	copy2, err := s.Duplicate(ctx, updated.ID)
	if err != nil || copy2.ID != "coder-renamed-copy-2" { t.Fatalf("copy2=%+v err=%v", copy2, err) }

	if err := s.Delete(ctx, updated.ID); err != nil { t.Fatal(err) }
	if err := s.Delete(ctx, updated.ID); err == nil { t.Fatal("second delete should fail") }
}

func TestCreateAndUpdateErrors(t *testing.T) {
	ctx := context.Background()
	s, db := testService(t)
	if _, err := s.Create(ctx, CreateInput{ModelID: "missing", Name: "Nope"}); err == nil { t.Fatal("missing model should fail") }
	if _, err := s.Update(ctx, "missing", UpdateInput{Name: "Nope", ModelID: "m1"}); err == nil { t.Fatal("missing instance update should fail") }
	if _, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "One"}); err != nil { t.Fatal(err) }
	if _, err := s.Create(ctx, CreateInput{ModelID: "m1", Name: "One"}); err == nil { t.Fatal("duplicate slug should fail") }
	if err := db.Close(); err != nil { t.Fatal(err) }
	if _, err := s.List(ctx); err == nil { t.Fatal("closed db list should fail") }
	if _, err := s.Options(ctx, "one"); err == nil { t.Fatal("closed db options should fail") }
	if err := s.Delete(ctx, "one"); err == nil { t.Fatal("closed db delete should fail") }
}
