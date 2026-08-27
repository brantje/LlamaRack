package models

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestModelCreateAndUpdateValidationBranches(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	validPath := writeGGUF(t, dir, "validation-Q4_K_M.gguf")

	createCases := []struct {
		name string
		in   CreateModelInput
	}{
		{"missing name", CreateModelInput{GGUFPath: validPath}},
		{"negative context", CreateModelInput{Name: "Bad", GGUFPath: validPath, ContextLength: -1}},
		{"missing path", CreateModelInput{Name: "Bad"}},
		{"missing file", CreateModelInput{Name: "Bad", GGUFPath: "missing.gguf"}},
	}
	for _, tc := range createCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Create(ctx, tc.in); err == nil {
				t.Fatal("expected create validation error")
			}
		})
	}

	outside := filepath.Join(t.TempDir(), "outside.gguf")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateModelInput{Name: "Outside", GGUFPath: outside}); err == nil {
		t.Fatal("expected outside-model-directory error")
	}
	if _, err := s.Create(ctx, CreateModelInput{Name: "Directory", GGUFPath: dir}); err == nil {
		t.Fatal("expected directory path error")
	}
	notGGUF := filepath.Join(dir, "model.bin")
	if err := os.WriteFile(notGGUF, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateModelInput{Name: "Wrong extension", GGUFPath: notGGUF}); err == nil {
		t.Fatal("expected GGUF extension error")
	}

	created, err := s.Create(ctx, CreateModelInput{Name: "Valid", GGUFPath: validPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, CreateModelInput{Name: "Duplicate", GGUFPath: validPath}); err == nil {
		t.Fatal("expected duplicate GGUF error")
	}

	legacyPath := writeGGUF(t, dir, "legacy-validation.gguf")
	if _, err := s.Create(ctx, CreateModelInput{Name: "Legacy bad id", GGUFPath: legacyPath, PublicID: "bad id"}); err == nil {
		t.Fatal("expected invalid legacy model id error")
	}
	idlePath := writeGGUF(t, dir, "legacy-idle.gguf")
	if _, err := s.Create(ctx, CreateModelInput{Name: "Legacy bad idle", GGUFPath: idlePath, PublicID: "legacy-idle", IdleUnloadSeconds: -1}); err == nil {
		t.Fatal("expected negative idle unload error")
	}

	if _, err := s.Update(ctx, created.ID, UpdateModelInput{}); err == nil {
		t.Fatal("expected update name validation error")
	}
	if _, err := s.Update(ctx, created.ID, UpdateModelInput{Name: "Updated", ContextLength: -1}); err == nil {
		t.Fatal("expected update context validation error")
	}
	if _, err := s.Update(ctx, "missing-id", UpdateModelInput{Name: "Missing"}); err == nil {
		t.Fatal("expected missing model error")
	}
}

func TestModelAndSidecarPathSafetyBranches(t *testing.T) {
	s, dir := testModelService(t)
	if _, err := s.ModelAbsolutePath(Model{GGUFPath: "../outside.gguf"}); err == nil {
		t.Fatal("expected model path escape error")
	}

	valid := filepath.Join(dir, "helper.gguf")
	if err := os.WriteFile(valid, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := sidecarAbsolutePath(dir, "helper.gguf"); !ok || filepath.Clean(got) != filepath.Clean(valid) {
		t.Fatalf("valid helper path=%q ok=%v", got, ok)
	}
	if _, ok := sidecarAbsolutePath(dir, "missing.gguf"); ok {
		t.Fatal("missing sidecar should be rejected")
	}
	if _, ok := sidecarAbsolutePath(dir, "."); ok {
		t.Fatal("directory sidecar should be rejected")
	}
	if _, ok := sidecarAbsolutePath(dir, "../escape.gguf"); ok {
		t.Fatal("escaping sidecar should be rejected")
	}

	if _, err := os.Stat(valid); errors.Is(err, os.ErrNotExist) {
		t.Fatal("valid helper unexpectedly missing")
	}
}
