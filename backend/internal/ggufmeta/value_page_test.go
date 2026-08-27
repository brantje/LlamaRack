package ggufmeta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestReadValuePageLazilyPagesLongStringAndArray(t *testing.T) {
	long := strings.Repeat("abcdef", 1000)
	items := []kv{
		{"long.value", 8, func(b *bytes.Buffer) { writeString(b, long) }},
		{"numbers", 9, func(b *bytes.Buffer) {
			write(b, uint32(4))
			write(b, uint64(250))
			for index := 0; index < 250; index++ {
				write(b, uint32(index))
			}
		}},
		{"scalar", 4, func(b *bytes.Buffer) { write(b, uint32(42)) }},
	}
	path := writeGGUF(t, 3, 0, items)

	text, err := ReadValuePage(path, "long.value", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if text.Type != "string" || text.Value != long[10:30] || text.Offset != 10 || text.Limit != 20 || text.Total != uint64(len(long)) || !text.HasMore {
		t.Fatalf("string page=%+v", text)
	}

	array, err := ReadValuePage(path, "numbers", 100, 5)
	if err != nil {
		t.Fatal(err)
	}
	if array.Type != "array<uint32>" || array.Offset != 100 || array.Limit != 5 || array.Total != 250 || len(array.Items) != 5 || array.Items[0] != "100" || array.Items[4] != "104" || !array.HasMore {
		t.Fatalf("array page=%+v", array)
	}
	clamped, err := ReadValuePage(path, "numbers", 249, 9999)
	if err != nil {
		t.Fatal(err)
	}
	if clamped.Limit != maxValuePageArrayItems || len(clamped.Items) != 1 || clamped.Items[0] != "249" || clamped.HasMore {
		t.Fatalf("clamped page=%+v", clamped)
	}

	scalar, err := ReadValuePage(path, "scalar", 99, 99)
	if err != nil || scalar.Type != "uint32" || scalar.Value != "42" || scalar.Total != 1 {
		t.Fatalf("scalar page=%+v err=%v", scalar, err)
	}
	if _, err := ReadValuePage(path, "missing", 0, 0); !errors.Is(err, ErrMetadataKeyNotFound) {
		t.Fatalf("missing err=%v", err)
	}
}

func TestReadValuePageRejectsInvalidInput(t *testing.T) {
	path := writeGGUF(t, 3, 0, []kv{{"x", 4, func(b *bytes.Buffer) { _ = binary.Write(b, binary.LittleEndian, uint32(1)) }}})
	if _, err := ReadValuePage(path, "", 0, 0); err == nil {
		t.Fatal("empty key should fail")
	}
}
