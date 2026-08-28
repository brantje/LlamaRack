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
	if derived.Architecture != "llama" || derived.ContextLength != 131072 || derived.BlockCount != 32 || derived.Embedding != 4096 || derived.HeadCount != 32 || derived.KVHeadCount != 8 {
		t.Fatalf("derived=%+v", derived)
	}
	if _, err := client.DerivedMetadata(context.Background(), detail); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("metadata requests=%d, want cached single request", got)
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
	writeDiscoveryBinary(t, &b, uint64(7))
	writeDiscoveryString(t, &b, "general.architecture")
	writeDiscoveryBinary(t, &b, uint32(8))
	writeDiscoveryString(t, &b, "llama")
	for _, item := range []struct {
		key   string
		value int64
	}{
		{"llama.context_length", 131072},
		{"llama.block_count", 32},
		{"llama.embedding_length", 4096},
		{"llama.attention.head_count", 32},
		{"llama.attention.head_count_kv", 8},
	} {
		writeDiscoveryString(t, &b, item.key)
		writeDiscoveryBinary(t, &b, uint32(11))
		writeDiscoveryBinary(t, &b, item.value)
	}
	writeDiscoveryString(t, &b, "tokenizer.ggml.tokens")
	return b.Bytes()
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
