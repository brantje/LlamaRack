package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMetadataSummaryServicePaths(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := benchmarkWriteGGUF(t, dir, "metadata-Q4_K_M.gguf", "llama", 32768, false)

	contextLength, err := s.DetectContext(path)
	if err != nil || contextLength != 32768 {
		t.Fatalf("DetectContext=%d err=%v", contextLength, err)
	}
	features, err := s.DetectGGUFFeatures(path)
	if err != nil || features.Architecture != "llama" || features.Projector || features.HasMTP {
		t.Fatalf("DetectGGUFFeatures=%+v err=%v", features, err)
	}
	if _, err := s.DetectContext(filepath.Join(dir, "missing.gguf")); err == nil {
		t.Fatal("DetectContext should reject a missing file")
	}
	if _, err := s.DetectGGUFFeatures(filepath.Join(dir, "missing.gguf")); err == nil {
		t.Fatal("DetectGGUFFeatures should reject a missing file")
	}

	model, err := s.Create(ctx, CreateModelInput{Name: "Metadata", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := s.RefreshDetectedContext(ctx, model.ID)
	if err != nil || refreshed.ContextLength != 32768 {
		t.Fatalf("RefreshDetectedContext=%+v err=%v", refreshed, err)
	}
	// An explicit/stored context is authoritative and must not require the file
	// to remain readable on subsequent reconciliation.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	refreshed, err = s.RefreshDetectedContext(ctx, model.ID)
	if err != nil || refreshed.ContextLength != 32768 {
		t.Fatalf("explicit context refresh=%+v err=%v", refreshed, err)
	}

	unregister := s.RegisterDetectedLlamaDefaults()
	unregister()
}

func TestDetectedLlamaDefaultsUsesIndexedMTPFeatures(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := writeClassifiedGGUF(t, dir, "native-mtp.gguf", "qwen35", 1, true)
	model, err := s.Create(ctx, CreateModelInput{Name: "Native MTP", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := s.DetectedLlamaDefaults(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if defaults["spec-type"] != "draft-mtp" || defaults["spec-draft-n-max"] != "16" || defaults["spec-draft-p-min"] != "0.8" {
		t.Fatalf("defaults=%+v", defaults)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// The persisted fingerprint summary remains usable for unchanged model
	// metadata calls only while the file still passes normal path validation;
	// missing backing files deliberately degrade to no detected defaults.
	defaults, err = s.DetectedLlamaDefaults(ctx, model.ID)
	if err != nil || defaults != nil {
		t.Fatalf("missing-file defaults=%+v err=%v", defaults, err)
	}
}
