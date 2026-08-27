package models

import (
	"path/filepath"
	"testing"
)

func TestReadGGUFValueUsesValidatedSharedInspectorPath(t *testing.T) {
	s, dir := testModelService(t)
	path := filepath.Join(dir, "value.gguf")
	writeMetadataModel(t, path, "qwen2", 32768)

	page, err := s.ReadGGUFValue(path, "general.architecture", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Key != "general.architecture" || page.Type != "string" || page.Value != "qwen2" {
		t.Fatalf("page=%+v", page)
	}
	if _, err := s.ReadGGUFValue(filepath.Join(t.TempDir(), "outside.gguf"), "general.architecture", 0, 0); err == nil {
		t.Fatal("outside model path should fail validation")
	}
}

func TestLogicalGGUFSizeSingleAndNoopRefresh(t *testing.T) {
	ctx := t.Context()
	s, dir := testModelService(t)
	path := filepath.Join(dir, "single.gguf")
	writeMetadataModel(t, path, "llama", 4096)
	model, err := s.Create(ctx, CreateModelInput{Name: "Single", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}
	size, err := s.LogicalGGUFSize(path)
	if err != nil || size != model.TotalBytes {
		t.Fatalf("size=%d model=%d err=%v", size, model.TotalBytes, err)
	}
	refreshed, err := s.RefreshLogicalSize(ctx, model.ID)
	if err != nil || refreshed.TotalBytes != model.TotalBytes {
		t.Fatalf("refreshed=%+v err=%v", refreshed, err)
	}
}
