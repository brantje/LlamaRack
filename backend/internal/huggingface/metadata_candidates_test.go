package huggingface

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestDiscoveryMetadataCandidatesPreferPracticalTargetArtifacts(t *testing.T) {
	artifacts := []Artifact{
		{Name: "model-draft-Q8_0.gguf", Quantization: "Q8_0", ModelBytes: 2, ShardCount: 1, ExpectedShards: 1, Complete: true, Files: []File{{Path: "model-draft-Q8_0.gguf"}}},
		{Name: "model-vision-bf16.gguf", Quantization: "BF16", ModelBytes: 1, ShardCount: 1, ExpectedShards: 1, Complete: true, Files: []File{{Path: "model-vision-bf16.gguf"}}},
		{Name: "model-bf16.gguf", Quantization: "BF16", ModelBytes: 100, ShardCount: 4, ExpectedShards: 4, Complete: true, Files: []File{{Path: "model-bf16-00001-of-00004.gguf"}}},
		{Name: "model-Q5_K_M.gguf", Quantization: "Q5_K_M", ModelBytes: 20, ShardCount: 1, ExpectedShards: 1, Complete: true, Files: []File{{Path: "model-Q5_K_M.gguf"}}},
		{Name: "model-Q4_K_M.gguf", Quantization: "Q4_K_M", ModelBytes: 18, ShardCount: 1, ExpectedShards: 1, Complete: true, Files: []File{{Path: "model-Q4_K_M.gguf"}}},
	}
	got := discoveryMetadataCandidates(artifacts)
	want := []string{"model-Q4_K_M.gguf", "model-Q5_K_M.gguf", "model-bf16-00001-of-00004.gguf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates=%v want=%v", got, want)
	}
}

func TestDerivedMetadataRetriesAlternativeTargetArtifact(t *testing.T) {
	valid := discoveryGGUF(t)
	var preferredRequests atomic.Int32
	var fallbackRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/demo/resolve/candidate-fallback/model-Q4_K_M.gguf":
			preferredRequests.Add(1)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("not-a-gguf"))
		case "/acme/demo/resolve/candidate-fallback/model-Q5_K_M.gguf":
			fallbackRequests.Add(1)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(valid)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	detail := ModelDetail{
		ID: "acme/demo", Revision: "candidate-fallback",
		GGUF: &GGUFInfo{Architecture: "qwen35", ContextLength: 262144},
		Artifacts: []Artifact{
			{Name: "model-Q4_K_M.gguf", Quantization: "Q4_K_M", ModelBytes: 100, ShardCount: 1, ExpectedShards: 1, Complete: true, Files: []File{{Path: "model-Q4_K_M.gguf"}}},
			{Name: "model-Q5_K_M.gguf", Quantization: "Q5_K_M", ModelBytes: 120, ShardCount: 1, ExpectedShards: 1, Complete: true, Files: []File{{Path: "model-Q5_K_M.gguf"}}},
		},
	}
	derived, err := client.DerivedMetadata(context.Background(), detail)
	if err != nil {
		t.Fatal(err)
	}
	if preferredRequests.Load() != 1 || fallbackRequests.Load() != 1 {
		t.Fatalf("requests preferred=%d fallback=%d", preferredRequests.Load(), fallbackRequests.Load())
	}
	if derived.Architecture != "qwen35" || derived.ContextLength != 262144 || derived.BlockCount == 0 || derived.Embedding == 0 || derived.HeadCount == 0 {
		t.Fatalf("derived=%+v", derived)
	}
}

func TestArtifactRoleTokenUsesBoundaries(t *testing.T) {
	for _, name := range []string{"model-draft-Q4.gguf", "vision-model.gguf", "dir/vision/model.gguf"} {
		if !hasArtifactRoleToken(name, "draft") && !hasArtifactRoleToken(name, "vision") {
			t.Fatalf("expected role token in %q", name)
		}
	}
	if hasArtifactRoleToken("revision-model-Q4.gguf", "vision") {
		t.Fatal("revision must not be treated as vision")
	}
}
