package ggufmeta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func TestMetadataCountCeiling(t *testing.T) {
	if _, err := Inspect(writeHeader(t, 3, 0, maxMetadataCount-1)); err == nil {
		t.Fatal("truncated count just below ceiling should fail to parse")
	} else if strings.Contains(err.Error(), "metadata count") {
		t.Fatalf("just below ceiling should not fail the count check: %v", err)
	}
	if _, err := Inspect(writeHeader(t, 3, 0, maxMetadataCount)); err == nil {
		t.Fatal("truncated count at ceiling should fail to parse")
	} else if strings.Contains(err.Error(), "metadata count") {
		t.Fatalf("at ceiling should not fail the count check: %v", err)
	}
	if _, err := Inspect(writeHeader(t, 3, 0, maxMetadataCount+1)); err == nil || !strings.Contains(err.Error(), "metadata count") {
		t.Fatalf("above ceiling err=%v", err)
	}
	if _, err := Inspect(writeHeader(t, 3, 0, 1_000_000)); err == nil || !strings.Contains(err.Error(), "metadata count") {
		t.Fatalf("previously accepted million-entry count err=%v", err)
	}

	largeHeader := writeHeader(t, 3, 0, 100_000)
	for _, fn := range []struct {
		name string
		call func(path string) error
	}{
		{"Inspect", func(path string) error { _, err := Inspect(path); return err }},
		{"ReadSummary", func(path string) error { _, err := ReadSummary(path); return err }},
		{"ReadValuePage", func(path string) error { _, err := ReadValuePage(path, "x", 0, 0); return err }},
	} {
		if err := fn.call(largeHeader); err == nil || !strings.Contains(err.Error(), "metadata count") {
			t.Fatalf("%s previously accepted large count err=%v", fn.name, err)
		}
	}
	data, err := os.ReadFile(largeHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InspectDerivedReader(bytes.NewReader(data)); err == nil || !strings.Contains(err.Error(), "metadata count") {
		t.Fatalf("InspectDerivedReader previously accepted large count err=%v", err)
	}
}

func TestInspectRejectsRetainedMetadataBudget(t *testing.T) {
	n := int(maxRetainedMetadataBytes/maxKeyBytes) + 2
	items := make([]kv, n)
	key := strings.Repeat("k", int(maxKeyBytes))
	for i := range items {
		items[i] = kv{key, 0, func(b *bytes.Buffer) { b.WriteByte(1) }}
	}
	path := writeGGUF(t, 3, 0, items)
	if _, err := Inspect(path); !errors.Is(err, errMetadataBudgetExceeded) {
		t.Fatalf("retained budget err=%v", err)
	}
	if _, err := ReadSummary(path); !errors.Is(err, errMetadataBudgetExceeded) {
		t.Fatalf("ReadSummary retained budget err=%v", err)
	}
	if _, err := ReadValuePage(path, "missing", 0, 0); !errors.Is(err, errMetadataBudgetExceeded) {
		t.Fatalf("ReadValuePage retained budget err=%v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InspectDerivedReader(bytes.NewReader(data)); !errors.Is(err, errMetadataBudgetExceeded) {
		t.Fatalf("InspectDerivedReader retained budget err=%v", err)
	}
}

func TestCountingReaderReadFullDoesNotBypassSectionBudget(t *testing.T) {
	budget := &metadataBudget{consumed: maxMetadataSectionBytes - 8}
	r := &countingReader{r: bytes.NewReader(bytes.Repeat([]byte("x"), 64)), budget: budget}
	buf := make([]byte, 32)
	n, err := io.ReadFull(r, buf)
	if !errors.Is(err, errMetadataBudgetExceeded) {
		t.Fatalf("ReadFull n=%d err=%v", n, err)
	}
	if n > 8 {
		t.Fatalf("ReadFull consumed more than remaining budget: n=%d", n)
	}
	if budget.consumed > maxMetadataSectionBytes {
		t.Fatalf("consumed=%d", budget.consumed)
	}

	exhausted := &countingReader{r: bytes.NewReader(bytes.Repeat([]byte("x"), 8)), budget: &metadataBudget{consumed: maxMetadataSectionBytes}}
	if n, err := io.ReadFull(exhausted, make([]byte, 8)); n != 0 || !errors.Is(err, errMetadataBudgetExceeded) {
		t.Fatalf("exhausted ReadFull n=%d err=%v", n, err)
	}
}

func TestInspectRejectsMetadataSectionBudget(t *testing.T) {
	const stringBytes = uint64(16 * 1024 * 1024)
	const entries = 5
	if _, err := inspect(bufferedReader(hugeStringMetadataReader(entries, stringBytes))); !errors.Is(err, errMetadataBudgetExceeded) {
		t.Fatalf("Inspect section budget err=%v", err)
	}
	if _, err := readSummary(bufferedReader(hugeStringMetadataReader(entries, stringBytes))); !errors.Is(err, errMetadataBudgetExceeded) {
		t.Fatalf("ReadSummary section budget err=%v", err)
	}
	if _, err := InspectDerivedReader(hugeStringMetadataReader(entries, stringBytes)); !errors.Is(err, errMetadataBudgetExceeded) {
		t.Fatalf("InspectDerivedReader section budget err=%v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func hugeStringMetadataReader(count int, stringBytes uint64) io.Reader {
	var header bytes.Buffer
	header.WriteString("GGUF")
	_ = binary.Write(&header, binary.LittleEndian, uint32(3))
	_ = binary.Write(&header, binary.LittleEndian, uint64(0))
	_ = binary.Write(&header, binary.LittleEndian, uint64(count))
	parts := []io.Reader{bytes.NewReader(header.Bytes())}
	for i := 0; i < count; i++ {
		var meta bytes.Buffer
		key := fmt.Sprintf("k%d", i)
		_ = binary.Write(&meta, binary.LittleEndian, uint64(len(key)))
		meta.WriteString(key)
		_ = binary.Write(&meta, binary.LittleEndian, uint32(8))
		_ = binary.Write(&meta, binary.LittleEndian, stringBytes)
		parts = append(parts, bytes.NewReader(meta.Bytes()), io.LimitReader(zeroReader{}, int64(stringBytes)))
	}
	return io.MultiReader(parts...)
}
