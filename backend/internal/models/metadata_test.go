package models

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInspectGGUFDetectsContextAndRefreshPreservesExplicitValue(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	path := filepath.Join(dir, "metadata-Q4_K_M.gguf")
	writeMetadataModel(t, path, "qwen2", 32768)

	inspection, err := s.InspectGGUF(path)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Derived.Architecture != "qwen2" || inspection.Derived.ContextLength != 32768 {
		t.Fatalf("inspection=%+v", inspection)
	}
	if detected, err := s.DetectContext(path); err != nil || detected != 32768 {
		t.Fatalf("detected=%d err=%v", detected, err)
	}

	m, err := s.Create(ctx, CreateModelInput{Name: "Metadata", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if m.ContextLength != 0 {
		t.Fatalf("pre-refresh context=%d", m.ContextLength)
	}
	refreshed, err := s.RefreshDetectedContext(ctx, m.ID)
	if err != nil || refreshed.ContextLength != 32768 {
		t.Fatalf("refreshed=%+v err=%v", refreshed, err)
	}

	explicitPath := filepath.Join(dir, "explicit-Q5_K_M.gguf")
	writeMetadataModel(t, explicitPath, "gemma3", 131072)
	explicit, err := s.Create(ctx, CreateModelInput{Name: "Explicit", GGUFPath: explicitPath, ContextLength: 8192})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err = s.RefreshDetectedContext(ctx, explicit.ID)
	if err != nil || explicit.ContextLength != 8192 {
		t.Fatalf("explicit=%+v err=%v", explicit, err)
	}
}

func TestInspectGGUFValidationAndMissingContext(t *testing.T) {
	s, dir := testModelService(t)
	bad := filepath.Join(dir, "bad.gguf")
	if err := os.WriteFile(bad, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InspectGGUF(bad); err == nil {
		t.Fatal("malformed GGUF should fail inspection")
	}
	if _, err := s.InspectGGUF(filepath.Join(t.TempDir(), "outside.gguf")); err == nil {
		t.Fatal("outside path should fail")
	}

	path := filepath.Join(dir, "no-context.gguf")
	writeMetadataModel(t, path, "custom", 0)
	if detected, err := s.DetectContext(path); err != nil || detected != 0 {
		t.Fatalf("detected=%d err=%v", detected, err)
	}
	if safeContextInt(-1) != 0 || safeContextInt(0) != 0 || safeContextInt(4096) != 4096 {
		t.Fatal("safe context conversion")
	}
}

func TestRefreshUnknownContextsSkipsUnavailableAndRefreshesReadableModels(t *testing.T) {
	ctx := context.Background()
	s, dir := testModelService(t)
	validPath := filepath.Join(dir, "valid.gguf")
	writeMetadataModel(t, validPath, "llama", 65536)
	valid, err := s.Create(ctx, CreateModelInput{Name: "Valid", GGUFPath: validPath})
	if err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(dir, "bad.gguf")
	if err := os.WriteFile(badPath, []byte("not gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad, err := s.Create(ctx, CreateModelInput{Name: "Bad", GGUFPath: badPath})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.RefreshUnknownContexts(ctx); err != nil {
		t.Fatal(err)
	}
	valid, _ = s.GetByID(ctx, valid.ID)
	bad, _ = s.GetByID(ctx, bad.ID)
	if valid.ContextLength != 65536 {
		t.Fatalf("valid=%+v", valid)
	}
	if bad.ContextLength != 0 {
		t.Fatalf("bad=%+v", bad)
	}
}

func TestRunMetadataReconcilerRefreshesFileWhenMetadataBecomesAvailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, dir := testModelService(t)
	path := filepath.Join(dir, "pending.gguf")
	if err := os.WriteFile(path, []byte("pending"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := s.Create(ctx, CreateModelInput{Name: "Pending", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() { s.RunMetadataReconciler(ctx, time.Millisecond); close(done) }()
	writeMetadataModel(t, path, "qwen2", 32768)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, getErr := s.GetByID(context.Background(), model.ID)
		if getErr == nil && current.ContextLength == 32768 {
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("reconciler did not stop")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("reconciler did not detect context")
}

func TestRunMetadataReconcilerAcceptsDefaultIntervalAndStopsOnCancelledContext(t *testing.T) {
	s, _ := testModelService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.RunMetadataReconciler(ctx, 0)
}

func TestRefreshUnknownContextsReturnsListError(t *testing.T) {
	s, _ := testModelService(t)
	if err := s.DB().Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshUnknownContexts(context.Background()); err == nil {
		t.Fatal("closed database should fail list")
	}
}

func writeMetadataModel(t *testing.T, path, architecture string, contextLength int64) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	mustMetadataWrite(t, &b, uint32(3))
	mustMetadataWrite(t, &b, uint64(0))
	count := uint64(1)
	if contextLength > 0 {
		count++
	}
	mustMetadataWrite(t, &b, count)
	metadataString(t, &b, "general.architecture")
	mustMetadataWrite(t, &b, uint32(8))
	metadataString(t, &b, architecture)
	if contextLength > 0 {
		metadataString(t, &b, architecture+".context_length")
		mustMetadataWrite(t, &b, uint32(11))
		mustMetadataWrite(t, &b, contextLength)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func metadataString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	mustMetadataWrite(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func mustMetadataWrite(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
