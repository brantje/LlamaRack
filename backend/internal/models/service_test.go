package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/database"
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
		{Name: "Name", GGUFPath: valid},
		{PublicID: "bad id", Name: "Name", GGUFPath: valid},
		{PublicID: "bad/id", Name: "Name", GGUFPath: valid},
		{PublicID: "model", GGUFPath: valid},
		{PublicID: "model", Name: "Name", GGUFPath: ""},
		{PublicID: "model", Name: "Name", GGUFPath: outside},
		{PublicID: "model", Name: "Name", GGUFPath: dir},
		{PublicID: "model", Name: "Name", GGUFPath: bad},
		{PublicID: "model", Name: "Name", GGUFPath: filepath.Join(dir, "missing.gguf")},
		{PublicID: "model", Name: "Name", GGUFPath: valid, Priority: "urgent"},
		{PublicID: "model", Name: "Name", GGUFPath: valid, RoutingPolicy: "mystery"},
	} {
		if _, err := s.Create(ctx, tc); err == nil {
			t.Fatalf("expected create validation error for %+v", tc)
		}
	}
}

func TestCreateGetListOptionsInstancesAndDelete(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := writeGGUF(t, dir, "coder-IQ2_XS.gguf")
	autoload := false
	m, err := s.Create(ctx, CreateModelInput{
		PublicID: "coder",
		Name: "Coder Model",
		GGUFPath: path,
		Autoload: &autoload,
		AlwaysOn: true,
		Priority: "high",
		RoutingPolicy: "round_robin",
		Options: map[string]string{"ctx-size": "4096", "flash-attn": "true", "": "ignored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.PublicID != "coder" || m.Name != "Coder Model" || m.GGUFPath != "coder-IQ2_XS.gguf" || m.TotalBytes == 0 || m.Quantization != "IQ2_XS" {
		t.Fatalf("unexpected model identity: %+v", m)
	}
	if m.Autoload || !m.AlwaysOn || !m.Enabled || m.Priority != "high" || m.RoutingPolicy != "round_robin" {
		t.Fatalf("unexpected model settings: %+v", m)
	}

	byID, err := s.GetByID(ctx, m.ID)
	if err != nil || byID.PublicID != m.PublicID || byID.Name != m.Name {
		t.Fatalf("GetByID=%+v err=%v", byID, err)
	}
	byPublic, err := s.GetByPublicID(ctx, "coder")
	if err != nil || byPublic.ID != m.ID {
		t.Fatalf("GetByPublicID=%+v err=%v", byPublic, err)
	}
	items, err := s.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("List=%+v err=%v", items, err)
	}
	opts, err := s.Options(ctx, m.ID)
	if err != nil || opts["ctx-size"] != "4096" || opts["flash-attn"] != "true" || len(opts) != 2 {
		t.Fatalf("Options=%+v err=%v", opts, err)
	}
	instances, err := s.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 || instances[0].Name != "default" || !instances[0].Enabled || instances[0].GPUMode != "auto" {
		t.Fatalf("Instances=%+v err=%v", instances, err)
	}
	abs, err := s.ModelAbsolutePath(m)
	if err != nil || abs != path {
		t.Fatalf("absolute path=%q err=%v want=%q", abs, err, path)
	}

	if _, err := s.Create(ctx, CreateModelInput{PublicID: "coder", Name: "Duplicate", GGUFPath: path}); err == nil {
		t.Fatal("expected duplicate public id error")
	}
	if _, err := s.Create(ctx, CreateModelInput{PublicID: "coder-2", Name: "Second", GGUFPath: path}); err != nil {
		t.Fatalf("same GGUF should support multiple model configs: %v", err)
	}
	if err := s.Delete(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByID(ctx, m.ID); err == nil {
		t.Fatal("deleted model should not exist")
	}
}

func TestDefaultModelSettingsAndHelpers(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := writeGGUF(t, dir, "plain-f16.gguf")
	m, err := s.Create(ctx, CreateModelInput{PublicID: "plain", Name: "Plain", GGUFPath: "plain-f16.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Autoload || m.Priority != "normal" || m.RoutingPolicy != "least_active" || m.Quantization != "F16" {
		t.Fatalf("unexpected defaults: %+v", m)
	}
	if abs, err := s.ModelAbsolutePath(m); err != nil || abs != path {
		t.Fatalf("relative GGUF resolution=%q err=%v", abs, err)
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
