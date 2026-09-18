package cache

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type failingCache struct{}

func (failingCache) Get(context.Context, string, any) (bool, error) { return false, errors.New("cache unavailable") }
func (failingCache) Set(context.Context, string, any, time.Duration) error { return errors.New("cache unavailable") }
func (failingCache) Delete(context.Context, string) error { return errors.New("cache unavailable") }

func TestMemoryHitMissTTLAndSerialization(t *testing.T) {
	ctx := context.Background()
	memory := NewMemory()
	now := time.Unix(100, 0)
	memory.now = func() time.Time { return now }

	var value struct{ Name string }
	if hit, err := memory.Get(ctx, "missing", &value); err != nil || hit {
		t.Fatalf("miss hit=%v err=%v", hit, err)
	}
	if err := memory.Set(ctx, "key", struct{ Name string }{Name: "cached"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if hit, err := memory.Get(ctx, "key", &value); err != nil || !hit || value.Name != "cached" {
		t.Fatalf("hit=%v value=%+v err=%v", hit, value, err)
	}
	now = now.Add(2 * time.Minute)
	if hit, err := memory.Get(ctx, "key", &value); err != nil || hit {
		t.Fatalf("expired hit=%v err=%v", hit, err)
	}
}

func TestLayeredFallsThroughAndRepopulatesL1(t *testing.T) {
	ctx := context.Background()
	l1 := NewMemory()
	l2 := NewMemory()
	if err := l2.Set(ctx, "key", map[string]string{"value": "l2"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	layered := NewLayered(l1, l2)
	var got map[string]string
	if hit, err := layered.Get(ctx, "key", &got); err != nil || !hit || got["value"] != "l2" {
		t.Fatalf("layered hit=%v got=%v err=%v", hit, got, err)
	}
	var l1Got map[string]string
	if hit, err := l1.Get(ctx, "key", &l1Got); err != nil || !hit || l1Got["value"] != "l2" {
		t.Fatalf("l1 repopulation hit=%v got=%v err=%v", hit, l1Got, err)
	}
}

func TestLayeredReportsOptionalBackendFailure(t *testing.T) {
	layered := NewLayered(NewMemory(), failingCache{})
	var got map[string]string
	if hit, err := layered.Get(context.Background(), "key", &got); hit || err == nil {
		t.Fatalf("expected miss with cache error: hit=%v err=%v", hit, err)
	}
}

func TestRedisURLParseDoesNotLeakSecret(t *testing.T) {
	_, err := NewRedis("redis://:super-secret@%")
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("Redis URL parse error leaked secret: %v", err)
	}
}

func TestRedisHitMissMalformedAndTimeout(t *testing.T) {
	rawURL := os.Getenv("LLAMARACK_TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("LLAMARACK_TEST_REDIS_URL is not configured")
	}
	ctx := context.Background()
	cache, err := NewRedis(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	if err := cache.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	key := "llamarack:test:cache"
	_ = cache.Delete(ctx, key)
	defer cache.Delete(ctx, key)

	var got map[string]string
	if hit, err := cache.Get(ctx, key, &got); err != nil || hit {
		t.Fatalf("Redis miss hit=%v err=%v", hit, err)
	}
	if err := cache.Set(ctx, key, map[string]string{"value": "redis"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if hit, err := cache.Get(ctx, key, &got); err != nil || !hit || got["value"] != "redis" {
		t.Fatalf("Redis hit=%v got=%v err=%v", hit, got, err)
	}

	options, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	direct := redis.NewClient(options)
	defer direct.Close()
	malformed, _ := json.Marshal("not the expected object")
	if err := direct.Set(ctx, key, malformed, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	got = nil
	if hit, err := cache.Get(ctx, key, &got); hit || err == nil {
		t.Fatalf("malformed Redis value hit=%v err=%v", hit, err)
	}

	unavailable, err := NewRedis("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	defer unavailable.Close()
	started := time.Now()
	if hit, err := unavailable.Get(ctx, "key", &got); hit || err == nil {
		t.Fatalf("unavailable Redis hit=%v err=%v", hit, err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("unavailable Redis exceeded bounded timeout: %v", elapsed)
	}
}

func TestObservedMetrics(t *testing.T) {
	namespace := "test-" + time.Now().Format("150405.000000000")
	observed := NewObserved(namespace, "memory", NewMemory())
	ctx := context.Background()
	var got map[string]string
	_, _ = observed.Get(ctx, "missing", &got)
	_ = observed.Set(ctx, "key", map[string]string{"value": "x"}, time.Minute)
	_, _ = observed.Get(ctx, "key", &got)
	_ = observed.Delete(ctx, "key")
	var found *MetricsSnapshot
	for _, snapshot := range Metrics() {
		if snapshot.Namespace == namespace && snapshot.Backend == "memory" {
			copy := snapshot
			found = &copy
		}
	}
	if found == nil || found.Hits != 1 || found.Misses != 1 || found.Writes != 1 || found.Deletes != 1 || found.GetDurationNS == 0 {
		t.Fatalf("metrics=%+v", found)
	}
}
