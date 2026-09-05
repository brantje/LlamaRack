package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/database"
)

func testModelService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	modelsDir := filepath.Join(root, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, modelsDir), modelsDir
}

func writeGGUF(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("gguf-test-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCreateValidation(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	valid := writeGGUF(t, dir, "valid-Q4_K_M.gguf")
	outside := writeGGUF(t, t.TempDir(), "outside.gguf")
	bad := filepath.Join(dir, "model.bin")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []CreateModelInput{
		{Name: "", GGUFPath: valid},
		{Name: "Name", GGUFPath: ""},
		{Name: "Name", GGUFPath: outside},
		{Name: "Name", GGUFPath: dir},
		{Name: "Name", GGUFPath: bad},
		{Name: "Name", GGUFPath: filepath.Join(dir, "missing.gguf")},
		{Name: "Name", GGUFPath: valid, ContextLength: -1},
	} {
		if _, err := s.Create(ctx, tc); err == nil {
			t.Fatalf("expected create validation error for %+v", tc)
		}
	}

	if _, err := s.Create(ctx, CreateModelInput{Name: "Valid", GGUFPath: valid}); err != nil {
		t.Fatalf("plain registry model should be valid: %v", err)
	}
}

func TestAvailableGGUFsRecursiveAndExcludesRegistered(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	rootFile := writeGGUF(t, dir, "alpha-Q4_K_M.gguf")
	nestedDir := filepath.Join(dir, "Qwen", "coder")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGGUF(t, nestedDir, "beta-Q8_0.GGUF")
	if err := os.WriteFile(filepath.Join(nestedDir, "ignore.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := s.AvailableGGUFs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("available files=%+v", files)
	}
	if files[0].Path != "Qwen/coder/beta-Q8_0.GGUF" || files[0].Quantization != "Q8_0" || files[0].TotalBytes == 0 {
		t.Fatalf("unexpected nested discovery: %+v", files[0])
	}
	if files[1].Path != "alpha-Q4_K_M.gguf" || strings.Contains(files[1].Path, "/models/") {
		t.Fatalf("unexpected relative path: %+v", files[1])
	}

	if _, err := s.Create(ctx, CreateModelInput{Name: "Alpha", GGUFPath: rootFile}); err != nil {
		t.Fatal(err)
	}
	files, err = s.AvailableGGUFs(ctx)
	if err != nil || len(files) != 1 || files[0].Path != "Qwen/coder/beta-Q8_0.GGUF" {
		t.Fatalf("available after registration=%+v err=%v", files, err)
	}
}

func TestRegistryCreateGetUpdateOptionsAndDelete(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := writeGGUF(t, dir, "coder-IQ2_XS.gguf")
	m, err := s.Create(ctx, CreateModelInput{Name: "Coder Model", GGUFPath: path, ContextLength: 32768, Options: map[string]string{"ctx-size": "4096", "flash-attn": "true", "": "ignored"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == "" || m.Slug != "coder-model" || m.Name != "Coder Model" || m.GGUFPath != "coder-IQ2_XS.gguf" || m.TotalBytes == 0 || m.Quantization != "IQ2_XS" || m.ContextLength != 32768 {
		t.Fatalf("unexpected model: %+v", m)
	}
	if instances, err := s.Instances(ctx, m.ID); err != nil || len(instances) != 0 {
		t.Fatalf("new registry model must have zero instances: %+v err=%v", instances, err)
	}

	byID, err := s.GetByID(ctx, m.ID)
	if err != nil || byID.Name != m.Name {
		t.Fatalf("GetByID=%+v err=%v", byID, err)
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("List=%+v err=%v", items, err)
	}
	if items[0].PublicID != "" || items[0].RoutingPolicy != "" {
		t.Fatalf("registry-only List compatibility fields=%+v", items[0])
	}
	opts, err := s.Options(ctx, m.ID)
	if err != nil || opts["ctx-size"] != "4096" || opts["flash-attn"] != "true" || len(opts) != 2 {
		t.Fatalf("Options=%+v err=%v", opts, err)
	}

	updated, err := s.Update(ctx, m.ID, UpdateModelInput{Name: "Coder Updated", ContextLength: 65536, Options: map[string]string{"threads": "8"}})
	if err != nil || updated.ID != m.ID || updated.Slug != m.Slug || updated.Name != "Coder Updated" || updated.ContextLength != 65536 {
		t.Fatalf("Update=%+v err=%v", updated, err)
	}
	opts, _ = s.Options(ctx, m.ID)
	if len(opts) != 1 || opts["threads"] != "8" {
		t.Fatalf("updated options=%+v", opts)
	}
	if _, err := s.Update(ctx, m.ID, UpdateModelInput{Name: "", ContextLength: 1}); err == nil {
		t.Fatal("expected empty update name error")
	}
	if _, err := s.Update(ctx, m.ID, UpdateModelInput{Name: "x", ContextLength: -1}); err == nil {
		t.Fatal("expected negative context error")
	}
	if _, err := s.Update(ctx, "missing", UpdateModelInput{Name: "x"}); err == nil {
		t.Fatal("expected missing model error")
	}

	if _, err := s.Create(ctx, CreateModelInput{Name: "Duplicate file", GGUFPath: path}); err == nil || !strings.Contains(err.Error(), "already been added") {
		t.Fatalf("expected duplicate GGUF rejection, got %v", err)
	}
	if abs, err := s.ModelAbsolutePath(m); err != nil || abs != path {
		t.Fatalf("absolute path=%q err=%v want=%q", abs, err, path)
	}
	if err := s.Delete(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByID(ctx, m.ID); err == nil {
		t.Fatal("deleted model should not exist")
	}
}

func TestLegacyPublicIDCompatibilityCreatesAddressableInstance(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := writeGGUF(t, dir, "legacy-f16.gguf")
	autoload, eviction := false, false
	m, err := s.Create(ctx, CreateModelInput{PublicID: "legacy-model", Name: "Legacy", GGUFPath: path, Autoload: &autoload, AlwaysOn: true, Priority: "high", EvictionEnabled: &eviction, IdleUnloadSeconds: 90})
	if err != nil {
		t.Fatal(err)
	}
	instances, err := s.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	if instances[0].ID == "" || instances[0].ID == "legacy-model" || instances[0].Slug != "legacy-model" || instances[0].Autoload || !instances[0].AlwaysOn || instances[0].Priority != "high" || instances[0].EvictionEnabled || instances[0].IdleUnloadSeconds != 90 {
		t.Fatalf("legacy instance=%+v", instances[0])
	}
	resolved, err := s.GetByPublicID(ctx, "legacy-model")
	if err != nil || resolved.ID != m.ID || resolved.PublicID != "legacy-model" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}

	// List historically projects policy from the earliest Instance ordered by
	// created_at then id. Keep that exact compatibility behavior while batching.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO instances(id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,idle_unload_seconds,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"00000000-0000-4000-8000-000000000001", "legacy-first", m.ID, "Legacy First", 0, 1, 0, "low", 1, 17, 1); err != nil {
		t.Fatal(err)
	}
	expected, err := s.GetByID(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("List=%+v err=%v", items, err)
	}
	listed := items[0]
	if expected.PublicID != "legacy-first" || listed.PublicID != expected.PublicID || listed.Enabled != expected.Enabled || listed.Autoload != expected.Autoload || listed.AlwaysOn != expected.AlwaysOn || listed.Priority != expected.Priority || listed.EvictionEnabled != expected.EvictionEnabled || listed.IdleUnloadSeconds != expected.IdleUnloadSeconds || listed.RoutingPolicy != expected.RoutingPolicy {
		t.Fatalf("batched List legacy policy=%+v want=%+v", listed, expected)
	}

	if _, err := s.Create(ctx, CreateModelInput{PublicID: "bad id", Name: "Bad", GGUFPath: writeGGUF(t, dir, "bad.gguf")}); err == nil {
		t.Fatal("expected invalid legacy model id")
	}
}

func TestHelpersAndPathEscape(t *testing.T) {
	s, dir := testModelService(t)
	path := writeGGUF(t, dir, "plain-f16.gguf")
	m, err := s.Create(context.Background(), CreateModelInput{Name: "Plain", GGUFPath: "plain-f16.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	if abs, err := s.ModelAbsolutePath(m); err != nil || abs != path {
		t.Fatalf("relative resolution=%q err=%v", abs, err)
	}
	if quantFromName("foo-q8_0.gguf") != "Q8_0" || quantFromName("foo.BF16.gguf") != "BF16" || quantFromName("none.gguf") != "" {
		t.Fatal("quantization parsing mismatch")
	}
	if boolInt(true) != 1 || boolInt(false) != 0 {
		t.Fatal("boolInt mismatch")
	}
	if newID() == "" || newID() == newID() {
		t.Fatal("newID should produce non-empty unique values")
	}
	escaping := m
	escaping.GGUFPath = filepath.Join("..", "escape.gguf")
	if _, err := s.ModelAbsolutePath(escaping); err == nil {
		t.Fatal("expected path escape rejection")
	}
}
