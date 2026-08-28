package huggingface

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDerivedMetadataReadsRangeAndCachesByRevision(t *testing.T) {
	payload := discoveryGGUF(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/acme/demo/resolve/rev1/demo-Q4_K_M.gguf" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Range"); got != "bytes=0-8388607" {
			t.Fatalf("range=%q", got)
		}
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	detail := ModelDetail{ID: "acme/demo", Revision: "rev1", Artifacts: []Artifact{{
		ID: "q4", Complete: true, Files: []File{{Path: "demo-Q4_K_M.gguf", Size: int64(len(payload))}},
	}}}

	derived, err := client.DerivedMetadata(context.Background(), detail)
	if err != nil {
		t.Fatal(err)
	}
	if derived.Architecture != "gemma3" || derived.ContextLength != 131072 || derived.BlockCount != 34 || derived.Embedding != 2560 || derived.HeadCount != 8 || derived.KVHeadCount != 4 {
		t.Fatalf("derived=%+v", derived)
	}
	if _, err := client.DerivedMetadata(context.Background(), detail); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("metadata requests=%d, want cached single request", got)
	}
}

func TestDerivedMetadataRetriesWhenInitialRangeEndsInsideMetadata(t *testing.T) {
	payload := discoveryLargeGGUF(t)
	var ranges []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		w.WriteHeader(http.StatusPartialContent)
		limit := len(payload)
		if r.Header.Get("Range") == "bytes=0-8388607" && limit > int(discoveryMetadataLimit) {
			limit = int(discoveryMetadataLimit)
		}
		_, _ = w.Write(payload[:limit])
	}))
	defer server.Close()

	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	detail := ModelDetail{ID: "acme/large", Revision: "rev-large", Artifacts: []Artifact{{
		ID: "q4", Complete: true, Files: []File{{Path: "large-Q4_K_M.gguf", Size: int64(len(payload))}},
	}}}
	derived, err := client.DerivedMetadata(context.Background(), detail)
	if err != nil {
		t.Fatal(err)
	}
	if derived.Architecture != "llama" || derived.BlockCount != 32 || derived.Embedding != 4096 || derived.HeadCount != 32 || derived.KVHeadCount != 8 {
		t.Fatalf("derived=%+v", derived)
	}
	if len(ranges) != 2 || ranges[0] != "bytes=0-8388607" || ranges[1] != "bytes=0-49999999" {
		t.Fatalf("ranges=%v", ranges)
	}
}

func TestDerivedMetadataFailureBranches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "status.gguf") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("denied"))
			return
		}
		_, _ = w.Write([]byte("not-a-gguf"))
	}))
	defer server.Close()
	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.DerivedMetadata(context.Background(), ModelDetail{ID: "acme/demo", Revision: "empty"}); err == nil || !strings.Contains(err.Error(), "no complete model artifact") {
		t.Fatalf("empty error=%v", err)
	}
	invalid := ModelDetail{ID: "acme/demo", Revision: "invalid", Artifacts: []Artifact{{Complete: true, Files: []File{{Path: "invalid.gguf"}}}}}
	if _, err := client.DerivedMetadata(context.Background(), invalid); err == nil {
		t.Fatal("invalid GGUF should fail")
	}
	status := ModelDetail{ID: "acme/demo", Revision: "status", Artifacts: []Artifact{{Complete: true, Files: []File{{Path: "status.gguf"}}}}}
	if _, err := client.DerivedMetadata(context.Background(), status); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("status error=%v", err)
	}
}

func discoveryGGUF(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	writeDiscoveryBinary(t, &b, uint32(3))
	writeDiscoveryBinary(t, &b, uint64(0))
	writeDiscoveryBinary(t, &b, uint64(8))

	writeDiscoveryString(t, &b, "general.architecture")
	writeDiscoveryBinary(t, &b, uint32(8))
	writeDiscoveryString(t, &b, "gemma3")
	writeDiscoveryInt(t, &b, "gemma3.context_length", 131072)

	writeDiscoveryString(t, &b, "tokenizer.ggml.add_space_prefix")
	writeDiscoveryBinary(t, &b, uint32(7))
	writeDiscoveryBinary(t, &b, uint8(1))
	writeDiscoveryString(t, &b, "tokenizer.ggml.pre")
	writeDiscoveryBinary(t, &b, uint32(8))
	writeDiscoveryString(t, &b, "gemma")

	writeDiscoveryInt(t, &b, "gemma3.block_count", 34)
	writeDiscoveryInt(t, &b, "gemma3.embedding_length", 2560)
	writeDiscoveryInt(t, &b, "gemma3.attention.head_count", 8)
	writeDiscoveryInt(t, &b, "gemma3.attention.head_count_kv", 4)
	return b.Bytes()
}

func discoveryLargeGGUF(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	writeDiscoveryBinary(t, &b, uint32(3))
	writeDiscoveryBinary(t, &b, uint64(0))
	writeDiscoveryBinary(t, &b, uint64(7))

	writeDiscoveryString(t, &b, "general.architecture")
	writeDiscoveryBinary(t, &b, uint32(8))
	writeDiscoveryString(t, &b, "llama")
	writeDiscoveryInt(t, &b, "llama.context_length", 131072)

	writeDiscoveryString(t, &b, "tokenizer.chat_template")
	writeDiscoveryBinary(t, &b, uint32(8))
	writeDiscoveryString(t, &b, strings.Repeat("x", int(discoveryMetadataLimit)+1024))

	writeDiscoveryInt(t, &b, "llama.block_count", 32)
	writeDiscoveryInt(t, &b, "llama.embedding_length", 4096)
	writeDiscoveryInt(t, &b, "llama.attention.head_count", 32)
	writeDiscoveryInt(t, &b, "llama.attention.head_count_kv", 8)
	return b.Bytes()
}

func writeDiscoveryInt(t *testing.T, b *bytes.Buffer, key string, value int64) {
	t.Helper()
	writeDiscoveryString(t, b, key)
	writeDiscoveryBinary(t, b, uint32(11))
	writeDiscoveryBinary(t, b, value)
}

func writeDiscoveryString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	writeDiscoveryBinary(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func writeDiscoveryBinary(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
