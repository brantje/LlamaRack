package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceGPUParsingAndClosedDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	a, err := s.RegisterArtifact(ctx, writeGGUF(t, dir, "gpu-Q5_K_M.gguf"), "gpu")
	if err != nil { t.Fatal(err) }
	m, err := s.Create(ctx, CreateModelInput{PublicID: "gpu-model", ArtifactID: a.ID})
	if err != nil { t.Fatal(err) }
	if _, err := s.db.ExecContext(ctx, "UPDATE instances SET preferred=1,gpu_mode='manual',gpu_devices='0,1',tensor_split='1,1' WHERE model_id=?", m.ID); err != nil { t.Fatal(err) }
	instances, err := s.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 { t.Fatalf("instances=%+v err=%v", instances, err) }
	if !instances[0].Preferred || instances[0].GPUMode != "manual" || len(instances[0].GPUDevices) != 2 || instances[0].TensorSplit != "1,1" {
		t.Fatalf("unexpected instance: %+v", instances[0])
	}

	path := writeGGUF(t, dir, "after-close.gguf")
	if err := s.db.Close(); err != nil { t.Fatal(err) }
	if _, err := s.RegisterArtifact(ctx, path, "closed"); err == nil { t.Fatal("expected register DB error") }
	if _, err := s.ListArtifacts(ctx); err == nil { t.Fatal("expected list artifacts DB error") }
	if _, err := s.Create(ctx, CreateModelInput{PublicID:"closed", ArtifactID:a.ID}); err == nil { t.Fatal("expected create DB error") }
	if _, err := s.List(ctx); err == nil { t.Fatal("expected list DB error") }
	if _, err := s.GetByID(ctx, m.ID); err == nil { t.Fatal("expected get by id DB error") }
	if _, err := s.GetByPublicID(ctx, m.PublicID); err == nil { t.Fatal("expected get by public id DB error") }
	if err := s.Delete(ctx, m.ID); err == nil { t.Fatal("expected delete DB error") }
	if _, err := s.Options(ctx, m.ID); err == nil { t.Fatal("expected options DB error") }
	if _, err := s.Instances(ctx, m.ID); err == nil { t.Fatal("expected instances DB error") }
}

func TestRegisterArtifactRelativePathAndCustomDisplayName(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := filepath.Join(dir, "relative-Q6_K.gguf")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil { t.Fatal(err) }
	a, err := s.RegisterArtifact(ctx, "relative-Q6_K.gguf", "Custom")
	if err != nil { t.Fatal(err) }
	if a.DisplayName != "Custom" || a.LocalPath != "relative-Q6_K.gguf" || a.Quantization != "Q6_K" {
		t.Fatalf("artifact=%+v", a)
	}
}
