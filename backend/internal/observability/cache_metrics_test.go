package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appcache "github.com/brantje/llamarack/backend/internal/cache"
)

func TestMetricsExposeLowCardinalityCacheStats(t *testing.T) {
	namespace := "metrics-cache-test"
	observed := appcache.NewObserved(namespace, "memory", appcache.NewMemory())
	ctx := context.Background()
	key := "must-not-appear-in-metrics"
	var value map[string]string
	_, _ = observed.Get(ctx, key, &value)
	if err := observed.Set(ctx, key, map[string]string{"value": "cached"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if hit, err := observed.Get(ctx, key, &value); err != nil || !hit {
		t.Fatalf("cache hit=%v err=%v", hit, err)
	}

	handler := NewMetricsHandler(testService(t), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, expected := range []string{
		"llamarack_cache_hits_total{namespace=\"metrics-cache-test\",backend=\"memory\"} 1",
		"llamarack_cache_misses_total{namespace=\"metrics-cache-test\",backend=\"memory\"} 1",
		"llamarack_cache_origin_fetches_avoided_total{namespace=\"metrics-cache-test\",backend=\"memory\"} 1",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, key) {
		t.Fatalf("cache key leaked into metrics: %s", body)
	}
}
