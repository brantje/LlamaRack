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

func TestMemoryRejectsInvalidDestinationAndCorruptEntry(t *testing.T) {
	ctx := context.Background()
	var nilMemory *Memory
	var dst map[string]string
	if hit, err := nilMemory.Get(ctx, "key", &dst); hit || err == nil {
		t.Fatalf("nil memory Get hit=%v err=%v", hit, err)
	}
	if err := nilMemory.Set(ctx, "key", dst, time.Minute); err == nil {
		t.Fatal("nil memory Set succeeded")
	}
	if err := nilMemory.Delete(ctx, "key"); err != nil {
		t.Fatalf("nil memory Delete err=%v", err)
	}

	memory := NewMemory()
	if hit, err := memory.Get(ctx, "key", nil); hit || err == nil {
		t.Fatalf("nil destination hit=%v err=%v", hit, err)
	}
	memory.entries["corrupt"] = memoryEntry{payload: []byte("{not-json")}
	if hit, err := memory.Get(ctx, "corrupt", &dst); hit || err == nil {
		t.Fatalf("corrupt entry hit=%v err=%v", hit, err)
	}
	if _, ok := memory.entries["corrupt"]; ok {
		t.Fatal("corrupt cache entry was not removed")
	}
}

func TestLayeredSetDeleteAndL1Hit(t *testing.T) {
	ctx := context.Background()
	l1 := NewMemory()
	l2 := NewMemory()
	layered := NewLayered(l1, l2, time.Minute)

	if err := layered.Set(ctx, "key", map[string]string{"value": "both"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if hit, err := l1.Get(ctx, "key", &got); err != nil || !hit || got["value"] != "both" {
		t.Fatalf("l1 hit=%v got=%v err=%v", hit, got, err)
	}
	got = nil
	if hit, err := l2.Get(ctx, "key", &got); err != nil || !hit || got["value"] != "both" {
		t.Fatalf("l2 hit=%v got=%v err=%v", hit, got, err)
	}

	// A populated L1 must satisfy the lookup without consulting a failing L2.
	layered.l2 = failingCache{}
	got = nil
	if hit, err := layered.Get(ctx, "key", &got); err != nil || !hit || got["value"] != "both" {
		t.Fatalf("layered l1 hit=%v got=%v err=%v", hit, got, err)
	}
	layered.l2 = l2

	if err := layered.Delete(ctx, "key"); err != nil {
		t.Fatal(err)
	}
	for name, backend := range map[string]Cache{"l1": l1, "l2": l2} {
		got = nil
		if hit, err := backend.Get(ctx, "key", &got); err != nil || hit {
			t.Fatalf("%s delete hit=%v err=%v", name, hit, err)
		}
	}
}

func TestLayeredAndObservedPropagateBackendErrors(t *testing.T) {
	ctx := context.Background()
	layered := NewLayered(failingCache{}, failingCache{})
	if err := layered.Set(ctx, "key", "value", time.Minute); err == nil {
		t.Fatal("layered Set did not report backend errors")
	}
	if err := layered.Delete(ctx, "key"); err == nil {
		t.Fatal("layered Delete did not report backend errors")
	}

	observed := NewObserved("error-test-"+time.Now().Format("150405.000000000"), "failing", failingCache{})
	var dst string
	if hit, err := observed.Get(ctx, "key", &dst); hit || err == nil {
		t.Fatalf("observed Get hit=%v err=%v", hit, err)
	}
	if err := observed.Set(ctx, "key", "value", time.Minute); err == nil {
		t.Fatal("observed Set did not report error")
	}
	if err := observed.Delete(ctx, "key"); err == nil {
		t.Fatal("observed Delete did not report error")
	}
}

func TestNilRedisReceiverIsSafe(t *testing.T) {
	ctx := context.Background()
	var cache *Redis
	var dst map[string]string
	if hit, err := cache.Get(ctx, "key", &dst); hit || err == nil {
		t.Fatalf("nil Redis Get hit=%v err=%v", hit, err)
	}
	if err := cache.Set(ctx, "key", dst, time.Minute); err == nil {
		t.Fatal("nil Redis Set succeeded")
	}
	if err := cache.Delete(ctx, "key"); err != nil {
		t.Fatalf("nil Redis Delete err=%v", err)
	}
	if err := cache.Ping(ctx); err == nil {
		t.Fatal("nil Redis Ping succeeded")
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("nil Redis Close err=%v", err)
	}
}
