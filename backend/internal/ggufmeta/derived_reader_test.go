package ggufmeta

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func TestInspectDerivedReaderHandlesInterleavedTokenizerMetadata(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("GGUF")
	writeDerivedValue(t, &b, uint32(3))
	writeDerivedValue(t, &b, uint64(0))
	writeDerivedValue(t, &b, uint64(8))

	writeDerivedString(t, &b, "general.architecture")
	writeDerivedValue(t, &b, uint32(8))
	writeDerivedString(t, &b, "gemma3")

	writeDerivedString(t, &b, "gemma3.context_length")
	writeDerivedValue(t, &b, uint32(11))
	writeDerivedValue(t, &b, int64(131072))

	// Real GGUF files may interleave tokenizer metadata with architecture keys.
	// The parser must consume it and continue instead of treating tokenizer.* as
	// the end of useful metadata.
	writeDerivedString(t, &b, "tokenizer.ggml.tokens")
	writeDerivedValue(t, &b, uint32(9))
	writeDerivedValue(t, &b, uint32(8))
	writeDerivedValue(t, &b, uint64(3))
	for _, token := range []string{"<pad>", "hello", "world"} {
		writeDerivedString(t, &b, token)
	}

	writeDerivedString(t, &b, "tokenizer.ggml.add_space_prefix")
	writeDerivedValue(t, &b, uint32(7))
	writeDerivedValue(t, &b, uint8(1))

	for _, item := range []struct {
		key   string
		value int64
	}{
		{"gemma3.block_count", 34},
		{"gemma3.embedding_length", 2560},
		{"gemma3.attention.head_count", 8},
		{"gemma3.attention.head_count_kv", 4},
	} {
		writeDerivedString(t, &b, item.key)
		writeDerivedValue(t, &b, uint32(11))
		writeDerivedValue(t, &b, item.value)
	}

	derived, err := InspectDerivedReader(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if derived.Architecture != "gemma3" || derived.ContextLength != 131072 || derived.BlockCount != 34 || derived.Embedding != 2560 || derived.HeadCount != 8 || derived.KVHeadCount != 4 {
		t.Fatalf("derived=%+v", derived)
	}
}

func TestInspectDerivedReaderGracefulFailures(t *testing.T) {
	if _, err := InspectDerivedReader(strings.NewReader("nope")); err == nil {
		t.Fatal("invalid magic should fail")
	}
	if _, err := InspectDerivedReader(strings.NewReader("GGU")); err == nil {
		t.Fatal("short magic should fail")
	}
	if _, err := InspectDerivedReader(nil); err == nil {
		t.Fatal("nil reader should fail")
	}

	var b bytes.Buffer
	b.WriteString("GGUF")
	writeDerivedValue(t, &b, uint32(3))
	writeDerivedValue(t, &b, uint64(0))
	writeDerivedValue(t, &b, uint64(1))
	writeDerivedString(t, &b, "general.architecture")
	writeDerivedValue(t, &b, uint32(8))
	writeDerivedString(t, &b, "llama")
	derived, err := InspectDerivedReader(bytes.NewReader(b.Bytes()))
	if err == nil || derived.Architecture != "llama" {
		t.Fatalf("derived=%+v err=%v", derived, err)
	}
}

func TestInspectDerivedReaderHeaderAndMetadataErrors(t *testing.T) {
	build := func(version uint32, tensorCount *uint64, metadataCount *uint64, tail func(*bytes.Buffer)) []byte {
		var b bytes.Buffer
		b.WriteString("GGUF")
		writeDerivedValue(t, &b, version)
		if tensorCount != nil {
			writeDerivedValue(t, &b, *tensorCount)
		}
		if metadataCount != nil {
			writeDerivedValue(t, &b, *metadataCount)
		}
		if tail != nil {
			tail(&b)
		}
		return b.Bytes()
	}
	zero := uint64(0)
	one := uint64(1)
	tooMany := uint64(maxMetadataCount + 1)

	cases := []struct {
		name string
		data []byte
	}{
		{"unsupported-version", build(1, nil, nil, nil)},
		{"missing-tensor-count", build(3, nil, nil, nil)},
		{"missing-metadata-count", build(3, &zero, nil, nil)},
		{"unreasonable-metadata-count", build(3, &zero, &tooMany, nil)},
		{"missing-key", build(3, &zero, &one, nil)},
		{"missing-type", build(3, &zero, &one, func(b *bytes.Buffer) { writeDerivedString(t, b, "general.architecture") })},
		{"missing-value", build(3, &zero, &one, func(b *bytes.Buffer) {
			writeDerivedString(t, b, "general.architecture")
			writeDerivedValue(t, b, uint32(8))
		})},
		{"tokenizer-missing-type", build(3, &zero, &one, func(b *bytes.Buffer) { writeDerivedString(t, b, "tokenizer.ggml.tokens") })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := InspectDerivedReader(bytes.NewReader(tc.data)); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestInspectDerivedReaderCompletesWithoutTokenizer(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("GGUF")
	writeDerivedValue(t, &b, uint32(3))
	writeDerivedValue(t, &b, uint64(0))
	writeDerivedValue(t, &b, uint64(4))
	writeDerivedString(t, &b, "general.architecture")
	writeDerivedValue(t, &b, uint32(8))
	writeDerivedString(t, &b, "llama")
	for _, item := range []struct {
		key   string
		value int64
	}{
		{"llama.block_count", 32},
		{"llama.embedding_length", 4096},
		{"llama.attention.head_count", 32},
	} {
		writeDerivedString(t, &b, item.key)
		writeDerivedValue(t, &b, uint32(11))
		writeDerivedValue(t, &b, item.value)
	}
	derived, err := InspectDerivedReader(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !derivedCoreReady(derived) {
		t.Fatalf("derived=%+v", derived)
	}
}

func TestForwardReadSeeker(t *testing.T) {
	r := &forwardReadSeeker{reader: strings.NewReader("abcdef")}
	buf := make([]byte, 2)
	if n, err := r.Read(buf); err != nil || n != 2 || r.pos != 2 || string(buf) != "ab" {
		t.Fatalf("read n=%d pos=%d value=%q err=%v", n, r.pos, string(buf), err)
	}
	if pos, err := r.Seek(0, io.SeekCurrent); err != nil || pos != 2 {
		t.Fatalf("zero seek pos=%d err=%v", pos, err)
	}
	if _, err := r.Seek(-1, io.SeekCurrent); err == nil {
		t.Fatal("negative seek should fail")
	}
	if _, err := r.Seek(1, io.SeekStart); err == nil {
		t.Fatal("absolute seek should fail")
	}
	if pos, err := r.Seek(2, io.SeekCurrent); err != nil || pos != 4 {
		t.Fatalf("forward seek pos=%d err=%v", pos, err)
	}
	if pos, err := r.Seek(10, io.SeekCurrent); err == nil || pos != 6 {
		t.Fatalf("short forward seek pos=%d err=%v", pos, err)
	}
}

func writeDerivedString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	writeDerivedValue(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func writeDerivedValue(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
