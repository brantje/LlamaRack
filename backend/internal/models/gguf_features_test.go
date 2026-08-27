package models

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeClassifiedGGUF(t *testing.T, dir, name, architecture string, nextN uint32, trunk bool) string {
	t.Helper()
	var tensors []string
	if trunk {
		tensors = append(tensors, "blk.0.attn_norm.weight")
	}
	if nextN > 0 {
		tensors = append(tensors, "blk.40.nextn.eh_proj.weight")
	}
	if len(tensors) == 0 {
		tensors = append(tensors, "encoder.weight")
	}
	var b bytes.Buffer
	b.WriteString("GGUF")
	mustClassifiedWrite(t, &b, uint32(3))
	mustClassifiedWrite(t, &b, uint64(len(tensors)))
	metadataCount := uint64(1)
	if nextN > 0 {
		metadataCount++
	}
	mustClassifiedWrite(t, &b, metadataCount)
	classifiedString(t, &b, "general.architecture")
	mustClassifiedWrite(t, &b, uint32(8))
	classifiedString(t, &b, architecture)
	if nextN > 0 {
		classifiedString(t, &b, architecture+".nextn_predict_layers")
		mustClassifiedWrite(t, &b, uint32(4))
		mustClassifiedWrite(t, &b, nextN)
	}
	for _, tensor := range tensors {
		classifiedString(t, &b, tensor)
		mustClassifiedWrite(t, &b, uint32(1))
		mustClassifiedWrite(t, &b, uint64(1))
		mustClassifiedWrite(t, &b, uint32(0))
		mustClassifiedWrite(t, &b, uint64(0))
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func classifiedString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	mustClassifiedWrite(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func mustClassifiedWrite(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
