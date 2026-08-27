package models

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDetectedLlamaDefaultsForNativeAndSidecarMTP(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)

	nativePath := writeClassifiedGGUF(t, dir, "native.gguf", "qwen35", 1, true)
	native, err := s.Create(ctx, CreateModelInput{Name: "Native", GGUFPath: nativePath})
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := s.DetectedLlamaDefaults(ctx, native.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMTPDefaults(t, defaults)

	plainPath := writeClassifiedGGUF(t, dir, "plain.gguf", "qwen2", 0, true)
	plain, err := s.Create(ctx, CreateModelInput{Name: "Plain", GGUFPath: plainPath})
	if err != nil {
		t.Fatal(err)
	}
	defaults, err = s.DetectedLlamaDefaults(ctx, plain.ID)
	if err != nil || len(defaults) != 0 {
		t.Fatalf("plain defaults=%+v err=%v", defaults, err)
	}

	draftPath := writeClassifiedGGUF(t, dir, "draft.gguf", "qwen35", 1, false)
	mainPath := writeClassifiedGGUF(t, dir, "sidecar-main.gguf", "qwen2", 0, true)
	withDraft, err := s.Create(ctx, CreateModelInput{Name: "With draft", GGUFPath: mainPath, Options: map[string]string{"spec-draft-model": draftPath}})
	if err != nil {
		t.Fatal(err)
	}
	defaults, err = s.DetectedLlamaDefaults(ctx, withDraft.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertMTPDefaults(t, defaults)

	features, err := s.DetectGGUFFeatures(filepath.Base(nativePath))
	if err != nil || !features.HasMTP || features.MTPOnly {
		t.Fatalf("service features=%+v err=%v", features, err)
	}
}

func TestDetectedLlamaDefaultsToleratesUnreadableGGUF(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	bad := writeGGUF(t, dir, "bad.gguf")
	model, err := s.Create(ctx, CreateModelInput{Name: "Bad", GGUFPath: bad})
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := s.DetectedLlamaDefaults(ctx, model.ID)
	if err != nil || len(defaults) != 0 {
		t.Fatalf("defaults=%+v err=%v", defaults, err)
	}
}

func assertMTPDefaults(t *testing.T, defaults map[string]string) {
	t.Helper()
	if defaults["spec-type"] != "draft-mtp" || defaults["spec-draft-n-max"] != "16" || defaults["spec-draft-p-min"] != "0.8" {
		t.Fatalf("MTP defaults=%+v", defaults)
	}
}
