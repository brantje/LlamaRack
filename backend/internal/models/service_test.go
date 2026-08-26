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

func TestRegisterArtifactValidationAndListing(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	if _, err := s.RegisterArtifact(ctx, "", ""); err == nil {
		t.Fatal("expected required path error")
	}
	outside := writeGGUF(t, t.TempDir(), "outside-Q4_K_M.gguf")
	if _, err := s.RegisterArtifact(ctx, outside, ""); err == nil {
		t.Fatal("expected outside directory error")
	}
	if _, err := s.RegisterArtifact(ctx, dir, ""); err == nil {
		t.Fatal("expected directory error")
	}
	bad := filepath.Join(dir, "model.bin")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterArtifact(ctx, bad, ""); err == nil {
		t.Fatal("expected gguf extension error")
	}
	if _, err := s.RegisterArtifact(ctx, "missing.gguf", ""); err == nil {
		t.Fatal("expected missing file error")
	}

	path := writeGGUF(t, dir, "Example-Q4_K_M.gguf")
	a, err := s.RegisterArtifact(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.DisplayName != "Example-Q4_K_M.gguf" || a.LocalPath != "Example-Q4_K_M.gguf" || a.Quantization != "Q4_K_M" || a.TotalBytes == 0 {
		t.Fatalf("unexpected artifact: %+v", a)
	}
	if _, err := s.RegisterArtifact(ctx, path, "duplicate"); err == nil {
		t.Fatal("expected duplicate path error")
	}
	items, err := s.ListArtifacts(ctx)
	if err != nil || len(items) != 1 || items[0].ID != a.ID {
		t.Fatalf("artifacts=%+v err=%v", items, err)
	}
}

func TestCreateGetListOptionsInstancesAndDelete(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	a, err := s.RegisterArtifact(ctx, writeGGUF(t, dir, "coder-IQ2_XS.gguf"), "Coder")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []CreateModelInput{
		{PublicID: "", ArtifactID: a.ID},
		{PublicID: "bad id", ArtifactID: a.ID},
		{PublicID: "bad/id", ArtifactID: a.ID},
		{PublicID: "model", ArtifactID: ""},
		{PublicID: "model", ArtifactID: "missing"},
		{PublicID: "model", ArtifactID: a.ID, Priority: "urgent"},
	} {
		if _, err := s.Create(ctx, tc); err == nil {
			t.Fatalf("expected create validation error for %+v", tc)
		}
	}

	autoload := false
	m, err := s.Create(ctx, CreateModelInput{
		PublicID: "coder",
		DisplayName: "Coder Model",
		ArtifactID: a.ID,
		Autoload: &autoload,
		AlwaysOn: true,
		Priority: "high",
		RoutingPolicy: "round_robin",
		Options: map[string]string{"ctx-size": "4096", "flash-attn": "true", "": "ignored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.PublicID != "coder" || m.DisplayName != "Coder Model" || m.Autoload || !m.AlwaysOn || m.Priority != "high" || m.RoutingPolicy != "round_robin" {
		t.Fatalf("unexpected model: %+v", m)
	}
	if m.ArtifactPath != a.LocalPath || !m.Enabled {
		t.Fatalf("unexpected artifact/enabled fields: %+v", m)
	}

	byID, err := s.GetByID(ctx, m.ID)
	if err != nil || byID.PublicID != m.PublicID {
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
	abs, err := s.ArtifactAbsolutePath(m)
	if err != nil || abs != filepath.Join(dir, a.LocalPath) {
		t.Fatalf("absolute path=%q err=%v", abs, err)
	}

	if _, err := s.Create(ctx, CreateModelInput{PublicID: "coder", ArtifactID: a.ID}); err == nil {
		t.Fatal("expected duplicate public id error")
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
	a, err := s.RegisterArtifact(ctx, writeGGUF(t, dir, "plain-f16.gguf"), "plain")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Create(ctx, CreateModelInput{PublicID: "plain", ArtifactID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Autoload || m.Priority != "normal" || m.RoutingPolicy != "least_active" {
		t.Fatalf("unexpected defaults: %+v", m)
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
	escaping.ArtifactPath = filepath.Join("..", "escape.gguf")
	if _, err := s.ArtifactAbsolutePath(escaping); err == nil {
		t.Fatal("expected path escape rejection")
	}
}
