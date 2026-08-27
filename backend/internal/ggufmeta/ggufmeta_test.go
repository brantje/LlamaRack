package ggufmeta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type kv struct {
	key   string
	typ   uint32
	write func(*bytes.Buffer)
}

func TestInspectAllMetadataTypesAndDerived(t *testing.T) {
	items := []kv{
		{"general.architecture", 8, func(b *bytes.Buffer) { writeString(b, "qwen2") }},
		{"qwen2.context_length", 4, func(b *bytes.Buffer) { write(b, uint32(32768)) }},
		{"qwen2.block_count", 10, func(b *bytes.Buffer) { write(b, uint64(40)) }},
		{"qwen2.embedding_length", 11, func(b *bytes.Buffer) { write(b, int64(4096)) }},
		{"qwen2.attention.head_count", 2, func(b *bytes.Buffer) { write(b, uint16(32)) }},
		{"qwen2.attention.head_count_kv", 0, func(b *bytes.Buffer) { b.WriteByte(8) }},
		{"qwen2.attention.key_length", 3, func(b *bytes.Buffer) { write(b, int16(128)) }},
		{"qwen2.attention.value_length", 5, func(b *bytes.Buffer) { write(b, int32(128)) }},
		{"test.i8", 1, func(b *bytes.Buffer) { b.WriteByte(0xff) }},
		{"test.f32", 6, func(b *bytes.Buffer) { write(b, math.Float32bits(1.5)) }},
		{"test.bool", 7, func(b *bytes.Buffer) { b.WriteByte(1) }},
		{"test.f64", 12, func(b *bytes.Buffer) { write(b, math.Float64bits(2.5)) }},
		{"test.array", 9, func(b *bytes.Buffer) {
			write(b, uint32(4)); write(b, uint64(20)); for i := 0; i < 20; i++ { write(b, uint32(i)) }
		}},
		{"test.strings", 9, func(b *bytes.Buffer) { write(b, uint32(8)); write(b, uint64(2)); writeString(b, "a"); writeString(b, "b") }},
	}
	path := writeGGUF(t, 3, 7, items)
	got, err := Inspect(path)
	if err != nil { t.Fatal(err) }
	if got.Version != 3 || got.TensorCount != 7 || got.MetadataCount != uint64(len(items)) { t.Fatalf("header=%+v", got) }
	if got.Derived.Architecture != "qwen2" || got.Derived.ContextLength != 32768 || got.Derived.BlockCount != 40 || got.Derived.Embedding != 4096 || got.Derived.HeadCount != 32 || got.Derived.KVHeadCount != 8 || got.Derived.KeyLength != 128 || got.Derived.ValueLength != 128 { t.Fatalf("derived=%+v", got.Derived) }
	byKey := map[string]Entry{}
	for _, e := range got.Metadata { byKey[e.Key] = e }
	if byKey["test.i8"].Value != "-1" || byKey["test.f32"].Value != "1.5" || byKey["test.bool"].Value != "true" || byKey["test.f64"].Value != "2.5" { t.Fatalf("scalars=%+v", byKey) }
	arr := byKey["test.array"]
	if arr.Type != "array<uint32>" || arr.ArrayLength != 20 || !arr.Truncated || !strings.Contains(arr.Value, "4 more") { t.Fatalf("array=%+v", arr) }
	if byKey["test.strings"].Value != `["a", "b"]` { t.Fatalf("string array=%+v", byKey["test.strings"]) }
	for i := 1; i < len(got.Metadata); i++ { if got.Metadata[i-1].Key > got.Metadata[i].Key { t.Fatal("metadata not sorted") } }
}

func TestInspectLongValuesAndFilter(t *testing.T) {
	long := strings.Repeat("x", int(maxDisplayBytes)+20)
	path := writeGGUF(t, 2, 0, []kv{
		{"long.value", 8, func(b *bytes.Buffer) { writeString(b, long) }},
		{"other", 4, func(b *bytes.Buffer) { write(b, uint32(2)) }},
	})
	got, err := Inspect(path)
	if err != nil { t.Fatal(err) }
	if !got.Metadata[0].Truncated || len(got.Metadata[0].Value) <= int(maxDisplayBytes) { t.Fatalf("long=%+v", got.Metadata[0]) }
	page, total := Filter(got.Metadata, "long", 0, 10)
	if total != 1 || len(page) != 1 || page[0].Key != "long.value" { t.Fatalf("filter=%v total=%d", page, total) }
	page, total = Filter(got.Metadata, "", 1, 1)
	if total != 2 || len(page) != 1 { t.Fatalf("page=%v total=%d", page, total) }
	page, _ = Filter(got.Metadata, "", 99, 1)
	if len(page) != 0 { t.Fatalf("past end=%v", page) }
	page, _ = Filter(got.Metadata, "", -1, 9999)
	if len(page) != 2 { t.Fatalf("clamped=%v", page) }
}

func TestInspectRejectsMalformedInput(t *testing.T) {
	dir := t.TempDir()
	if _, err := Inspect(filepath.Join(dir, "missing.gguf")); err == nil { t.Fatal("missing should fail") }
	bad := filepath.Join(dir, "bad.gguf")
	_ = os.WriteFile(bad, []byte("nope"), 0o644)
	if _, err := Inspect(bad); err == nil || !strings.Contains(err.Error(), "magic") { t.Fatalf("bad=%v", err) }
	unsupported := writeHeader(t, 1, 0, 0)
	if _, err := Inspect(unsupported); err == nil || !strings.Contains(err.Error(), "version") { t.Fatalf("version=%v", err) }
	count := writeHeader(t, 3, 0, maxMetadataCount+1)
	if _, err := Inspect(count); err == nil || !strings.Contains(err.Error(), "metadata count") { t.Fatalf("count=%v", err) }
	nested := writeGGUF(t, 3, 0, []kv{{"nested", 9, func(b *bytes.Buffer) { write(b, uint32(9)); write(b, uint64(1)) }}})
	if _, err := Inspect(nested); err == nil || !strings.Contains(err.Error(), "nested") { t.Fatalf("nested=%v", err) }
	hugeArray := writeGGUF(t, 3, 0, []kv{{"huge", 9, func(b *bytes.Buffer) { write(b, uint32(4)); write(b, maxArrayCount+1) }}})
	if _, err := Inspect(hugeArray); err == nil || !strings.Contains(err.Error(), "array") { t.Fatalf("array=%v", err) }
	unsupportedType := writeGGUF(t, 3, 0, []kv{{"bad", 99, func(*bytes.Buffer) {}}})
	if _, err := Inspect(unsupportedType); err == nil || !strings.Contains(err.Error(), "unsupported") { t.Fatalf("type=%v", err) }

	truncatedHeader := filepath.Join(dir, "header.gguf")
	_ = os.WriteFile(truncatedHeader, []byte("GGUF\x03\x00\x00\x00"), 0o644)
	if _, err := Inspect(truncatedHeader); err == nil { t.Fatal("truncated counts should fail") }

	var key bytes.Buffer
	key.WriteString("GGUF"); write(&key, uint32(3)); write(&key, uint64(0)); write(&key, uint64(1)); write(&key, maxKeyBytes+1)
	keyPath := filepath.Join(dir, "key.gguf"); _ = os.WriteFile(keyPath, key.Bytes(), 0o644)
	if _, err := Inspect(keyPath); err == nil || !strings.Contains(err.Error(), "key") { t.Fatalf("key=%v", err) }
}

func TestLowLevelSkipAndBounds(t *testing.T) {
	for _, typeID := range []uint32{0, 1, 2, 3, 4, 5, 6, 7, 10, 11, 12} {
		if _, ok := fixedSize(typeID); !ok { t.Fatalf("fixed type %d", typeID) }
		if _, ok := typeName(typeID); !ok { t.Fatalf("named type %d", typeID) }
	}
	if _, ok := fixedSize(8); ok { t.Fatal("string fixed") }
	if _, ok := typeName(99); ok { t.Fatal("unknown type") }

	fixed := bytes.NewReader(make([]byte, 8))
	if err := skipValue(fixed, 4); err != nil { t.Fatal(err) }
	pos, _ := fixed.Seek(0, io.SeekCurrent)
	if pos != 4 { t.Fatalf("fixed skip=%d", pos) }

	var text bytes.Buffer
	writeString(&text, "hello")
	if err := skipValue(bytes.NewReader(text.Bytes()), 8); err != nil { t.Fatal(err) }
	var huge bytes.Buffer
	write(&huge, maxStringBytes+1)
	if err := skipValue(bytes.NewReader(huge.Bytes()), 8); err == nil { t.Fatal("huge skip string") }
	if err := skipValue(bytes.NewReader(nil), 99); err == nil { t.Fatal("unsupported skip") }

	var manyStrings bytes.Buffer
	write(&manyStrings, uint32(8)); write(&manyStrings, uint64(maxArrayPreview+2))
	for i := uint64(0); i < maxArrayPreview+2; i++ { writeString(&manyStrings, "v") }
	array, err := readArray(bytes.NewReader(manyStrings.Bytes()))
	if err != nil || !array.truncated || array.arrayLength != maxArrayPreview+2 { t.Fatalf("strings=%+v err=%v", array, err) }

	var unsupported bytes.Buffer
	write(&unsupported, uint32(99)); write(&unsupported, uint64(0))
	if _, err := readArray(bytes.NewReader(unsupported.Bytes())); err == nil { t.Fatal("unsupported element") }
	if _, err := readArray(bytes.NewReader(nil)); err == nil { t.Fatal("missing element type") }

	if _, err := scalarResult("x", uint64(0), errors.New("boom")); err == nil { t.Fatal("scalar error") }
	if _, err := readUint(bytes.NewReader(nil), 4); err == nil { t.Fatal("truncated uint") }
	if _, err := readUint(bytes.NewReader(make([]byte, 3)), 3); err == nil { t.Fatal("invalid uint width") }
	if _, err := readInt(bytes.NewReader(make([]byte, 3)), 3); err == nil { t.Fatal("invalid int width") }

	var hugeString bytes.Buffer
	write(&hugeString, maxStringBytes+1)
	if _, _, err := readString(bytes.NewReader(hugeString.Bytes())); err == nil { t.Fatal("huge string") }
	var truncatedString bytes.Buffer
	write(&truncatedString, uint64(5)); truncatedString.WriteString("ab")
	if _, _, err := readString(bytes.NewReader(truncatedString.Bytes())); err == nil { t.Fatal("truncated string") }

	var truncatedKey bytes.Buffer
	write(&truncatedKey, uint64(5)); truncatedKey.WriteString("ab")
	if _, err := readKey(bytes.NewReader(truncatedKey.Bytes())); err == nil { t.Fatal("truncated key") }

	if _, err := readValue(bytes.NewReader(nil), 6); err == nil { t.Fatal("truncated float") }
	if _, err := readValue(bytes.NewReader(nil), 12); err == nil { t.Fatal("truncated double") }
}

func TestExactIntAndHelpers(t *testing.T) {
	if exactInt(map[string]string{"x": "9223372036854775807"}, "x") != math.MaxInt64 { t.Fatal("max int") }
	if exactInt(map[string]string{"x": "18446744073709551615"}, "x") != 0 || exactInt(map[string]string{"x": "bad"}, "x") != 0 || exactInt(nil, "x") != 0 { t.Fatal("invalid ints") }
	if derive(map[string]string{"general.architecture": ""}).ContextLength != 0 { t.Fatal("empty architecture") }
}

func writeGGUF(t *testing.T, version uint32, tensors uint64, items []kv) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	write(&b, version)
	write(&b, tensors)
	write(&b, uint64(len(items)))
	for _, item := range items { writeString(&b, item.key); write(&b, item.typ); item.write(&b) }
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil { t.Fatal(err) }
	return path
}

func writeHeader(t *testing.T, version uint32, tensors, metadata uint64) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	write(&b, version)
	write(&b, tensors)
	write(&b, metadata)
	path := filepath.Join(t.TempDir(), "header.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil { t.Fatal(err) }
	return path
}

func writeString(b *bytes.Buffer, s string) { write(b, uint64(len(s))); b.WriteString(s) }
func write(b *bytes.Buffer, v any) { if err := binary.Write(b, binary.LittleEndian, v); err != nil { panic(err) } }
