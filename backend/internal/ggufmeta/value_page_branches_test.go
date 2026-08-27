package ggufmeta

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadValuePageCoversBoundsAndHeaderFailures(t *testing.T) {
	if _, err := ReadValuePage(filepath.Join(t.TempDir(), "missing.gguf"), "x", 0, 0); err == nil {
		t.Fatal("missing file should fail")
	}
	bad := filepath.Join(t.TempDir(), "bad.gguf")
	if err := os.WriteFile(bad, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadValuePage(bad, "x", 0, 0); err == nil || !strings.Contains(err.Error(), "magic") {
		t.Fatalf("bad magic err=%v", err)
	}
	unsupported := writeHeader(t, 1, 0, 0)
	if _, err := ReadValuePage(unsupported, "x", 0, 0); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version err=%v", err)
	}
	tooMany := writeHeader(t, 3, 0, maxMetadataCount+1)
	if _, err := ReadValuePage(tooMany, "x", 0, 0); err == nil || !strings.Contains(err.Error(), "metadata count") {
		t.Fatalf("count err=%v", err)
	}

	path := writeGGUF(t, 3, 0, []kv{{"text", 8, func(b *bytes.Buffer) { writeString(b, "hello") }}})
	page, err := ReadValuePage(path, "text", 99, maxValuePageStringBytes+99)
	if err != nil {
		t.Fatal(err)
	}
	if page.Offset != 5 || page.Limit != maxValuePageStringBytes || page.Total != 5 || page.Value != "" || page.HasMore {
		t.Fatalf("bounded string=%+v", page)
	}
	page, err = ReadValuePage(path, "text", 0, 0)
	if err != nil || page.Limit != maxValuePageStringBytes || page.Value != "hello" {
		t.Fatalf("default string=%+v err=%v", page, err)
	}
}

func TestReadValuePageCoversArrayKindsAndErrors(t *testing.T) {
	stringsPath := writeGGUF(t, 3, 0, []kv{{"strings", 9, func(b *bytes.Buffer) {
		write(b, uint32(8))
		write(b, uint64(2))
		writeString(b, "a")
		writeString(b, "b")
	}}})
	page, err := ReadValuePage(stringsPath, "strings", 99, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Offset != 2 || page.Limit != maxValuePageArrayItems || page.Total != 2 || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("past-end array=%+v", page)
	}
	page, err = ReadValuePage(stringsPath, "strings", 0, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0] != `"a"` || !page.HasMore {
		t.Fatalf("string array=%+v err=%v", page, err)
	}

	nested := writeGGUF(t, 3, 0, []kv{{"nested", 9, func(b *bytes.Buffer) {
		write(b, uint32(9))
		write(b, uint64(1))
	}}})
	if _, err := ReadValuePage(nested, "nested", 0, 0); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("nested err=%v", err)
	}
	unsupported := writeGGUF(t, 3, 0, []kv{{"unsupported", 9, func(b *bytes.Buffer) {
		write(b, uint32(99))
		write(b, uint64(0))
	}}})
	if _, err := ReadValuePage(unsupported, "unsupported", 0, 0); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported err=%v", err)
	}
	huge := writeGGUF(t, 3, 0, []kv{{"huge", 9, func(b *bytes.Buffer) {
		write(b, uint32(4))
		write(b, maxArrayCount+1)
	}}})
	if _, err := ReadValuePage(huge, "huge", 0, 0); err == nil || !strings.Contains(err.Error(), "unreasonable") {
		t.Fatalf("huge err=%v", err)
	}
}
