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
	path := writeGGUF(t, dir, "gpu-Q5_K_M.gguf")
	m, err := s.Create(ctx, CreateModelInput{PublicID: "gpu-model", Name: "GPU model", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE instances SET gpu_mode='manual',gpu_devices='0,1',tensor_split='1,1' WHERE model_id=?", m.ID); err != nil {
		t.Fatal(err)
	}
	instances, err := s.Instances(ctx, m.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("instances=%+v err=%v", instances, err)
	}
	if instances[0].GPUMode != "manual" || len(instances[0].GPUDevices) != 2 || instances[0].TensorSplit != "1,1" {
		t.Fatalf("unexpected instance: %+v", instances[0])
	}

	closedPath := writeGGUF(t, dir, "after-close.gguf")
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateModelInput{PublicID: "closed", Name: "Closed", GGUFPath: closedPath}); err == nil {
		t.Fatal("expected create DB error")
	}
	if _, err := s.List(ctx); err == nil {
		t.Fatal("expected list DB error")
	}
	if _, err := s.GetByID(ctx, m.ID); err == nil {
		t.Fatal("expected get by id DB error")
	}
	if _, err := s.GetByPublicID(ctx, "gpu-model"); err == nil {
		t.Fatal("expected get by public id DB error")
	}
	if err := s.Delete(ctx, m.ID); err == nil {
		t.Fatal("expected delete DB error")
	}
	if _, err := s.Options(ctx, m.ID); err == nil {
		t.Fatal("expected options DB error")
	}
	if _, err := s.Instances(ctx, m.ID); err == nil {
		t.Fatal("expected instances DB error")
	}
}

func TestCreateRelativePathAndDerivedMetadata(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := filepath.Join(dir, "relative-Q6_K.gguf")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := s.Create(ctx, CreateModelInput{Name: "Custom", GGUFPath: "relative-Q6_K.gguf", ContextLength: 8192})
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Custom" || m.GGUFPath != "relative-Q6_K.gguf" || m.Quantization != "Q6_K" || m.TotalBytes != 3 || m.ContextLength != 8192 {
		t.Fatalf("model=%+v", m)
	}
}
