package models

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeClassifiedGGUF(tb testing.TB, dir, name, architecture string, nextN uint32, trunk bool) string {
	tb.Helper()
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
	mustClassifiedWrite(tb, &b, uint32(3))
	mustClassifiedWrite(tb, &b, uint64(len(tensors)))
	metadataCount := uint64(1)
	if nextN > 0 {
		metadataCount++
	}
	mustClassifiedWrite(tb, &b, metadataCount)
	classifiedString(tb, &b, "general.architecture")
	mustClassifiedWrite(tb, &b, uint32(8))
	classifiedString(tb, &b, architecture)
	if nextN > 0 {
		classifiedString(tb, &b, architecture+".nextn_predict_layers")
		mustClassifiedWrite(tb, &b, uint32(4))
		mustClassifiedWrite(tb, &b, nextN)
	}
	for _, tensor := range tensors {
		classifiedString(tb, &b, tensor)
		mustClassifiedWrite(tb, &b, uint32(1))
		mustClassifiedWrite(tb, &b, uint64(1))
		mustClassifiedWrite(tb, &b, uint32(0))
		mustClassifiedWrite(tb, &b, uint64(0))
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		tb.Fatal(err)
	}
	return path
}

func classifiedString(tb testing.TB, b *bytes.Buffer, value string) {
	tb.Helper()
	mustClassifiedWrite(tb, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func mustClassifiedWrite(tb testing.TB, b *bytes.Buffer, value any) {
	tb.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		tb.Fatal(err)
	}
}
