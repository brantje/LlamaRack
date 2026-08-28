package huggingface

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
)

func TestProviderDerivedOverridesHubOwnedFieldsOnly(t *testing.T) {
	parsed := ggufmeta.Derived{
		Architecture: "llama", ContextLength: 131072,
		BlockCount: 64, Embedding: 5120, HeadCount: 40, KVHeadCount: 8, KeyLength: 128, ValueLength: 128,
	}
	merged := providerDerived(parsed, &GGUFInfo{Architecture: "qwen35", ContextLength: 262144})
	if merged.Architecture != "qwen35" || merged.ContextLength != 262144 {
		t.Fatalf("provider fields not preferred: %+v", merged)
	}
	if merged.BlockCount != parsed.BlockCount || merged.Embedding != parsed.Embedding || merged.HeadCount != parsed.HeadCount || merged.KVHeadCount != parsed.KVHeadCount || merged.KeyLength != parsed.KeyLength || merged.ValueLength != parsed.ValueLength {
		t.Fatalf("low-level parser fields were replaced: %+v", merged)
	}
	if got := providerDerived(parsed, nil); got != parsed {
		t.Fatalf("nil provider changed metadata: %+v", got)
	}
}

func TestDerivedMetadataPrefersProviderArchitectureAndContext(t *testing.T) {
	payload := discoveryGGUF(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	detail := ModelDetail{
		ID: "acme/demo", Revision: "provider-preferred",
		GGUF: &GGUFInfo{Architecture: "qwen35", ContextLength: 262144},
		Artifacts: []Artifact{{Complete: true, Files: []File{{Path: "demo-Q4_K_M.gguf", Size: int64(len(payload))}}}},
	}
	derived, err := client.DerivedMetadata(context.Background(), detail)
	if err != nil {
		t.Fatal(err)
	}
	if derived.Architecture != "qwen35" || derived.ContextLength != 262144 {
		t.Fatalf("derived=%+v", derived)
	}
	if derived.BlockCount != 32 || derived.Embedding != 4096 || derived.HeadCount != 32 || derived.KVHeadCount != 8 {
		t.Fatalf("missing ranged low-level metadata: %+v", derived)
	}
}

func TestDerivedMetadataKeepsProviderSummaryWhenLowLevelMetadataUnavailable(t *testing.T) {
	detail := ModelDetail{ID: "acme/demo", Revision: "provider-only", GGUF: &GGUFInfo{Architecture: "qwen35", ContextLength: 262144}}
	derived, err := (&Client{}).DerivedMetadata(context.Background(), detail)
	if err == nil {
		t.Fatal("expected missing artifact error")
	}
	if derived.Architecture != "qwen35" || derived.ContextLength != 262144 {
		t.Fatalf("derived=%+v err=%v", derived, err)
	}
}
