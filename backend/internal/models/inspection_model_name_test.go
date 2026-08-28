package models

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectGGUFArtifactIncludesDetectedModelName(t *testing.T) {
	s, dir := testModelService(t)
	path := filepath.Join(dir, "model-Q4_K_M.gguf")
	writeNamedInspectionGGUF(t, path, "qwen2", "qwen coder 32b")

	inspection, err := s.InspectGGUFArtifact(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.ModelName != "qwen coder 32b" {
		t.Fatalf("model name = %q", inspection.ModelName)
	}
}

func writeNamedInspectionGGUF(t *testing.T, path, architecture, modelName string) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	mustNamedInspectionWrite(t, &b, uint32(3))
	mustNamedInspectionWrite(t, &b, uint64(0))
	mustNamedInspectionWrite(t, &b, uint64(2))
	writeNamedInspectionString(t, &b, "general.architecture")
	mustNamedInspectionWrite(t, &b, uint32(8))
	writeNamedInspectionString(t, &b, architecture)
	writeNamedInspectionString(t, &b, "general.name")
	mustNamedInspectionWrite(t, &b, uint32(8))
	writeNamedInspectionString(t, &b, modelName)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeNamedInspectionString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	mustNamedInspectionWrite(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func mustNamedInspectionWrite(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
