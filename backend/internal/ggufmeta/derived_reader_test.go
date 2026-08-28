package ggufmeta

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestInspectDerivedReaderStopsBeforeTokenizerPayload(t *testing.T) {
	var b bytes.Buffer
	b.WriteString("GGUF")
	writeDerivedValue(t, &b, uint32(3))
	writeDerivedValue(t, &b, uint64(0))
	writeDerivedValue(t, &b, uint64(7))
	writeDerivedString(t, &b, "general.architecture")
	writeDerivedValue(t, &b, uint32(8))
	writeDerivedString(t, &b, "llama")
	for key, value := range map[string]int64{
		"llama.context_length": 131072,
		"llama.block_count": 32,
		"llama.embedding_length": 4096,
		"llama.attention.head_count": 32,
		"llama.attention.head_count_kv": 8,
	} {
		writeDerivedString(t, &b, key)
		writeDerivedValue(t, &b, uint32(11))
		writeDerivedValue(t, &b, value)
	}
	// No type/value follows this key on purpose. The remote parser must return
	// before reading the huge tokenizer section.
	writeDerivedString(t, &b, "tokenizer.ggml.tokens")

	derived, err := InspectDerivedReader(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if derived.Architecture != "llama" || derived.ContextLength != 131072 || derived.BlockCount != 32 || derived.Embedding != 4096 || derived.HeadCount != 32 || derived.KVHeadCount != 8 {
		t.Fatalf("derived=%+v", derived)
	}
}

func TestInspectDerivedReaderGracefulFailures(t *testing.T) {
	if _, err := InspectDerivedReader(strings.NewReader("nope")); err == nil { t.Fatal("invalid magic should fail") }
	if _, err := InspectDerivedReader(nil); err == nil { t.Fatal("nil reader should fail") }

	var b bytes.Buffer
	b.WriteString("GGUF")
	writeDerivedValue(t, &b, uint32(3))
	writeDerivedValue(t, &b, uint64(0))
	writeDerivedValue(t, &b, uint64(1))
	writeDerivedString(t, &b, "general.architecture")
	writeDerivedValue(t, &b, uint32(8))
	writeDerivedString(t, &b, "llama")
	derived, err := InspectDerivedReader(bytes.NewReader(b.Bytes()))
	if err == nil || derived.Architecture != "llama" { t.Fatalf("derived=%+v err=%v", derived, err) }
}

func writeDerivedString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	writeDerivedValue(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func writeDerivedValue(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil { t.Fatal(err) }
}
