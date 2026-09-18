package cache

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Cache stores only recomputable, non-authoritative values. Callers own key
// semantics and TTL selection.
type Cache interface {
	Get(context.Context, string, any) (bool, error)
	Set(context.Context, string, any, time.Duration) error
	Delete(context.Context, string) error
}

type memoryEntry struct {
	payload   []byte
	expiresAt time.Time
}

// Memory is the zero-dependency default cache. Values are JSON encoded so the
// same serialization contract is exercised with and without Redis.
type Memory struct {
	mu      sync.RWMutex
	entries map[string]memoryEntry
	now     func() time.Time
}

func NewMemory() *Memory {
	return &Memory{entries: map[string]memoryEntry{}, now: time.Now}
}

func (m *Memory) Get(_ context.Context, key string, dst any) (bool, error) {
	if m == nil || dst == nil {
		return false, errors.New("cache destination is required")
	}
	m.mu.RLock()
	entry, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok {
		return false, nil
	}
	if !entry.expiresAt.IsZero() && !m.now().Before(entry.expiresAt) {
		m.mu.Lock()
		if current, exists := m.entries[key]; exists && current.expiresAt.Equal(entry.expiresAt) {
			delete(m.entries, key)
		}
		m.mu.Unlock()
		return false, nil
	}
	if err := json.Unmarshal(entry.payload, dst); err != nil {
		m.mu.Lock()
		delete(m.entries, key)
		m.mu.Unlock()
		return false, err
	}
	return true, nil
}

func (m *Memory) Set(_ context.Context, key string, value any, ttl time.Duration) error {
	if m == nil {
		return errors.New("memory cache is not configured")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = m.now().Add(ttl)
	}
	m.mu.Lock()
	m.entries[key] = memoryEntry{payload: payload, expiresAt: expiresAt}
	m.mu.Unlock()
	return nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	delete(m.entries, key)
	m.mu.Unlock()
	return nil
}

// Layered implements L1 -> optional L2 lookup while retaining L1 speed. L2
// failures are returned to the caller, which can choose the authoritative
// fallback; successful L2 hits repopulate L1.
type Layered struct {
	l1      Cache
	l2      Cache
	warmTTL time.Duration
}

func NewLayered(l1, l2 Cache, warmTTL ...time.Duration) *Layered {
	var ttl time.Duration
	if len(warmTTL) > 0 {
		ttl = warmTTL[0]
	}
	return &Layered{l1: l1, l2: l2, warmTTL: ttl}
}

func (c *Layered) Get(ctx context.Context, key string, dst any) (bool, error) {
	var errs []error
	if c != nil && c.l1 != nil {
		hit, err := c.l1.Get(ctx, key, dst)
		if hit {
			return true, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if c == nil || c.l2 == nil {
		return false, errors.Join(errs...)
	}
	hit, err := c.l2.Get(ctx, key, dst)
	if err != nil {
		errs = append(errs, err)
	}
	if !hit {
		return false, errors.Join(errs...)
	}
	if c.l1 != nil {
		// L1 repopulation should never turn a successful authoritative L2 hit
		// into a miss. Record the error but still return the value.
		if err := c.l1.Set(ctx, key, dst, c.warmTTL); err != nil {
			errs = append(errs, err)
		}
	}
	return true, errors.Join(errs...)
}

func (c *Layered) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	var errs []error
	if c != nil && c.l1 != nil {
		if err := c.l1.Set(ctx, key, value, ttl); err != nil {
			errs = append(errs, err)
		}
	}
	if c != nil && c.l2 != nil {
		if err := c.l2.Set(ctx, key, value, ttl); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Layered) Delete(ctx context.Context, key string) error {
	var errs []error
	if c != nil && c.l1 != nil {
		if err := c.l1.Delete(ctx, key); err != nil {
			errs = append(errs, err)
		}
	}
	if c != nil && c.l2 != nil {
		if err := c.l2.Delete(ctx, key); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type MetricsSnapshot struct {
	Namespace       string
	Backend         string
	Hits            uint64
	Misses          uint64
	Errors          uint64
	Writes          uint64
	Deletes         uint64
	GetDurationNS   uint64
	SetDurationNS   uint64
	DeleteDurationNS uint64
}

type metricsCounters struct {
	hits, misses, errors, writes, deletes atomic.Uint64
	getNS, setNS, deleteNS                atomic.Uint64
}

var metricsRegistry sync.Map
var originAvoidedRegistry sync.Map

type OriginFetchesAvoidedSnapshot struct {
	Namespace string
	Count     uint64
}

// RecordOriginFetchAvoided is called by cache consumers only after a cached
// value has passed semantic validation and the authoritative fetch is skipped.
func RecordOriginFetchAvoided(namespace string) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return
	}
	value, _ := originAvoidedRegistry.LoadOrStore(namespace, &atomic.Uint64{})
	value.(*atomic.Uint64).Add(1)
}

func OriginFetchesAvoidedMetrics() []OriginFetchesAvoidedSnapshot {
	var out []OriginFetchesAvoidedSnapshot
	originAvoidedRegistry.Range(func(rawKey, rawValue any) bool {
		namespace, _ := rawKey.(string)
		counter, _ := rawValue.(*atomic.Uint64)
		if namespace != "" && counter != nil {
			out = append(out, OriginFetchesAvoidedSnapshot{Namespace: namespace, Count: counter.Load()})
		}
		return true
	})
	return out
}

type Observed struct {
	namespace string
	backend   string
	inner     Cache
	metrics   *metricsCounters
}

func NewObserved(namespace, backend string, inner Cache) *Observed {
	key := namespace + "\x00" + backend
	value, _ := metricsRegistry.LoadOrStore(key, &metricsCounters{})
	return &Observed{namespace: namespace, backend: backend, inner: inner, metrics: value.(*metricsCounters)}
}

func (c *Observed) Get(ctx context.Context, key string, dst any) (bool, error) {
	if c == nil || c.inner == nil {
		return false, errors.New("cache backend is not configured")
	}
	started := time.Now()
	hit, err := c.inner.Get(ctx, key, dst)
	c.metrics.getNS.Add(uint64(time.Since(started).Nanoseconds()))
	if err != nil {
		c.metrics.errors.Add(1)
	} else if hit {
		c.metrics.hits.Add(1)
	} else {
		c.metrics.misses.Add(1)
	}
	return hit, err
}

func (c *Observed) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if c == nil || c.inner == nil {
		return errors.New("cache backend is not configured")
	}
	started := time.Now()
	err := c.inner.Set(ctx, key, value, ttl)
	c.metrics.setNS.Add(uint64(time.Since(started).Nanoseconds()))
	if err != nil {
		c.metrics.errors.Add(1)
	} else {
		c.metrics.writes.Add(1)
	}
	return err
}

func (c *Observed) Delete(ctx context.Context, key string) error {
	if c == nil || c.inner == nil {
		return errors.New("cache backend is not configured")
	}
	started := time.Now()
	err := c.inner.Delete(ctx, key)
	c.metrics.deleteNS.Add(uint64(time.Since(started).Nanoseconds()))
	if err != nil {
		c.metrics.errors.Add(1)
	} else {
		c.metrics.deletes.Add(1)
	}
	return err
}

func Metrics() []MetricsSnapshot {
	var out []MetricsSnapshot
	metricsRegistry.Range(func(rawKey, rawValue any) bool {
		key, _ := rawKey.(string)
		counters, _ := rawValue.(*metricsCounters)
		if counters == nil {
			return true
		}
		parts := []byte(key)
		index := -1
		for i, b := range parts {
			if b == 0 {
				index = i
				break
			}
		}
		namespace, backend := key, ""
		if index >= 0 {
			namespace, backend = key[:index], key[index+1:]
		}
		out = append(out, MetricsSnapshot{
			Namespace: namespace, Backend: backend,
			Hits: counters.hits.Load(), Misses: counters.misses.Load(), Errors: counters.errors.Load(),
			Writes: counters.writes.Load(), Deletes: counters.deletes.Load(),
			GetDurationNS: counters.getNS.Load(), SetDurationNS: counters.setNS.Load(), DeleteDurationNS: counters.deleteNS.Load(),
		})
		return true
	})
	return out
}
