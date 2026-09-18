package huggingface

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appcache "github.com/brantje/llamarack/backend/internal/cache"
)

type cacheFailure struct{}
type cacheMiss struct{}

func (cacheFailure) Get(context.Context, string, any) (bool, error) {
	return false, context.DeadlineExceeded
}
func (cacheFailure) Set(context.Context, string, any, time.Duration) error {
	return context.DeadlineExceeded
}
func (cacheFailure) Delete(context.Context, string) error { return context.DeadlineExceeded }

func (cacheMiss) Get(context.Context, string, any) (bool, error) { return false, nil }
func (cacheMiss) Set(context.Context, string, any, time.Duration) error { return nil }
func (cacheMiss) Delete(context.Context, string) error { return nil }

func TestDerivedMetadataCacheKeyIsVersionedOpaqueAndIdentitySensitive(t *testing.T) {
	client, err := NewClient("https://huggingface.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	detail := ModelDetail{ID: "private/secret-model", Revision: "revision-secret"}
	first := client.derivedMetadataCacheKey(detail, "model-secret-Q4_K_M.gguf")
	if !strings.HasPrefix(first, "llamarack:v1:hf:derived:") {
		t.Fatalf("cache key=%q", first)
	}
	for _, secret := range []string{"private/secret-model", "revision-secret", "model-secret-Q4_K_M.gguf", "huggingface.example"} {
		if strings.Contains(first, secret) {
			t.Fatalf("cache key leaked identity %q: %s", secret, first)
		}
	}
	changedRevision := detail
	changedRevision.Revision = "other"
	if second := client.derivedMetadataCacheKey(changedRevision, "model-secret-Q4_K_M.gguf"); second == first {
		t.Fatal("revision change did not change cache key")
	}
	if second := client.derivedMetadataCacheKey(detail, "other.gguf"); second == first {
		t.Fatal("artifact change did not change cache key")
	}
}

func TestDerivedMetadataCacheFailureFallsBackToOrigin(t *testing.T) {
	payload := discoveryGGUF(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.SetDerivedMetadataCache(cacheFailure{})
	detail := ModelDetail{ID: "acme/demo", Revision: "cache-failure", Artifacts: []Artifact{{
		ID: "q4", Complete: true, Files: []File{{Path: "demo-Q4_K_M.gguf", Size: int64(len(payload))}},
	}}}
	derived, err := client.DerivedMetadata(context.Background(), detail)
	if err != nil {
		t.Fatal(err)
	}
	if derived.Architecture != "gemma3" || requests.Load() != 1 {
		t.Fatalf("fallback derived=%+v requests=%d", derived, requests.Load())
	}
}

func TestDerivedMetadataRedisWarmHitSurvivesFreshClient(t *testing.T) {
	rawURL := os.Getenv("LLAMARACK_TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("LLAMARACK_TEST_REDIS_URL is not configured")
	}
	payload := discoveryGGUF(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	detail := ModelDetail{ID: "acme/redis-demo", Revision: "redis-revision", Artifacts: []Artifact{{
		ID: "q4", Complete: true, Files: []File{{Path: "redis-Q4_K_M.gguf", Size: int64(len(payload))}},
	}}}

	firstClient, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	firstRedis, err := appcache.NewRedis(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	key := firstClient.derivedMetadataCacheKey(detail, detail.Artifacts[0].Files[0].Path)
	_ = firstRedis.Delete(context.Background(), key)
	firstClient.SetDerivedMetadataCache(appcache.NewLayered(
		appcache.NewMemory(), firstRedis, discoveryMetadataCacheTTL,
	))
	if _, err := firstClient.DerivedMetadata(context.Background(), detail); err != nil {
		firstRedis.Close()
		t.Fatal(err)
	}
	if err := firstRedis.Close(); err != nil {
		t.Fatal(err)
	}

	secondClient, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	secondRedis, err := appcache.NewRedis(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer secondRedis.Close()
	defer secondRedis.Delete(context.Background(), key)
	secondClient.SetDerivedMetadataCache(appcache.NewLayered(
		appcache.NewMemory(), secondRedis, discoveryMetadataCacheTTL,
	))
	derived, err := secondClient.DerivedMetadata(context.Background(), detail)
	if err != nil {
		t.Fatal(err)
	}
	if derived.Architecture != "gemma3" {
		t.Fatalf("derived=%+v", derived)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("origin requests=%d, Redis warm hit should avoid the second fetch", got)
	}
}

func BenchmarkDerivedMetadataWarmMemoryCache(b *testing.B) {
	payload := discoveryGGUF(b)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		b.Fatal(err)
	}
	client.SetDerivedMetadataCache(appcache.NewMemory())
	detail := ModelDetail{ID: "acme/bench", Revision: "memory", Artifacts: []Artifact{{
		ID: "q4", Complete: true, Files: []File{{Path: "bench-Q4_K_M.gguf", Size: int64(len(payload))}},
	}}}
	if _, err := client.DerivedMetadata(context.Background(), detail); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.DerivedMetadata(context.Background(), detail); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDerivedMetadataOriginFetch(b *testing.B) {
	payload := discoveryGGUF(b)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		b.Fatal(err)
	}
	client.SetDerivedMetadataCache(cacheMiss{})
	detail := ModelDetail{ID: "acme/bench", Revision: "origin", Artifacts: []Artifact{{
		ID: "q4", Complete: true, Files: []File{{Path: "bench-Q4_K_M.gguf", Size: int64(len(payload))}},
	}}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.DerivedMetadata(context.Background(), detail); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDerivedMetadataWarmRedis(b *testing.B) {
	rawURL := os.Getenv("LLAMARACK_TEST_REDIS_URL")
	if rawURL == "" {
		b.Skip("LLAMARACK_TEST_REDIS_URL is not configured")
	}
	payload := discoveryGGUF(b)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	redisCache, err := appcache.NewRedis(rawURL)
	if err != nil {
		b.Fatal(err)
	}
	defer redisCache.Close()
	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		b.Fatal(err)
	}
	client.SetDerivedMetadataCache(redisCache)
	detail := ModelDetail{ID: "acme/bench", Revision: "redis", Artifacts: []Artifact{{
		ID: "q4", Complete: true, Files: []File{{Path: "bench-Q4_K_M.gguf", Size: int64(len(payload))}},
	}}}
	key := client.derivedMetadataCacheKey(detail, detail.Artifacts[0].Files[0].Path)
	_ = redisCache.Delete(context.Background(), key)
	defer redisCache.Delete(context.Background(), key)
	if _, err := client.DerivedMetadata(context.Background(), detail); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := client.DerivedMetadata(context.Background(), detail); err != nil {
			b.Fatal(err)
		}
	}
}
