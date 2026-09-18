package huggingface

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appcache "github.com/brantje/llamarack/backend/internal/cache"
	"github.com/brantje/llamarack/backend/internal/ggufmeta"
)

const liveQualificationRepo = "Qwen/Qwen2.5-0.5B-Instruct-GGUF"

type qualificationTraffic struct {
	rangeRequests atomic.Int64
	rangeBytes    atomic.Int64
}

type qualificationTransport struct {
	base    http.RoundTripper
	traffic *qualificationTraffic
}

func (t qualificationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if req.Header.Get("Range") != "" {
		t.traffic.rangeRequests.Add(1)
		resp.Body = &qualificationCountingBody{ReadCloser: resp.Body, bytes: &t.traffic.rangeBytes}
	}
	return resp, nil
}

type qualificationCountingBody struct {
	io.ReadCloser
	bytes *atomic.Int64
}

func (b *qualificationCountingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytes.Add(int64(n))
	return n, err
}

type qualificationPhase struct {
	duration      time.Duration
	rangeRequests int64
	rangeBytes    int64
}

func measureQualificationPhase(traffic *qualificationTraffic, fn func() error) (qualificationPhase, error) {
	beforeRequests := traffic.rangeRequests.Load()
	beforeBytes := traffic.rangeBytes.Load()
	started := time.Now()
	err := fn()
	return qualificationPhase{
		duration:      time.Since(started),
		rangeRequests: traffic.rangeRequests.Load() - beforeRequests,
		rangeBytes:    traffic.rangeBytes.Load() - beforeBytes,
	}, err
}

// TestLiveRedisDerivedMetadataQualification is an opt-in real-provider
// qualification for #162. It is intentionally excluded from ordinary local
// test runs because it measures the real Hugging Face network path. The
// dedicated Redis release-qualification workflow runs it as a release gate:
// warm shared-cache phases must avoid origin traffic and the fresh-client Redis
// hit must be materially faster than the cold authoritative lookup.
func TestLiveRedisDerivedMetadataQualification(t *testing.T) {
	if os.Getenv("LLAMARACK_LIVE_HF_QUALIFICATION") != "1" {
		t.Skip("set LLAMARACK_LIVE_HF_QUALIFICATION=1 to run real Hugging Face qualification")
	}
	rawRedis := os.Getenv("LLAMARACK_TEST_REDIS_URL")
	if rawRedis == "" {
		t.Skip("LLAMARACK_TEST_REDIS_URL is not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	detailClient, err := NewClient("https://huggingface.co", nil)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := detailClient.Detail(ctx, liveQualificationRepo)
	if err != nil {
		t.Fatalf("load live model detail: %v", err)
	}
	candidates := discoveryMetadataCandidates(detail.Artifacts)
	if len(candidates) == 0 {
		t.Fatal("live model has no metadata candidates")
	}

	redisCache, err := appcache.NewRedis(rawRedis)
	if err != nil {
		t.Fatal(err)
	}
	defer redisCache.Close()

	traffic := &qualificationTraffic{}
	httpClient := &http.Client{
		Transport: qualificationTransport{base: http.DefaultTransport, traffic: traffic},
		Timeout:   metadataOriginTimeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	newClient := func() *Client {
		client, err := NewClientWithHTTP("https://huggingface.co", nil, httpClient)
		if err != nil {
			t.Fatal(err)
		}
		client.SetDerivedMetadataCache(appcache.NewLayered(
			appcache.NewMemory(), redisCache, discoveryMetadataCacheTTL,
		))
		return client
	}

	first := newClient()
	key := first.derivedMetadataCacheKey(detail, candidates[0])
	if err := redisCache.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	defer redisCache.Delete(context.Background(), key)

	cold, err := measureQualificationPhase(traffic, func() error {
		derived, err := first.DerivedMetadata(ctx, detail)
		if err == nil && !ggufmeta.DerivedCoreReady(derived) {
			return fmt.Errorf("cold derived metadata incomplete")
		}
		return err
	})
	if err != nil {
		t.Fatalf("cold authoritative lookup: %v", err)
	}
	if cold.rangeRequests == 0 || cold.rangeBytes == 0 {
		t.Fatalf("cold lookup did not perform a measured range read: %+v", cold)
	}

	warmMemory, err := measureQualificationPhase(traffic, func() error {
		derived, err := first.DerivedMetadata(ctx, detail)
		if err == nil && !ggufmeta.DerivedCoreReady(derived) {
			return fmt.Errorf("warm-memory derived metadata incomplete")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if warmMemory.rangeRequests != 0 || warmMemory.rangeBytes != 0 {
		t.Fatalf("warm L1 unexpectedly reached origin: %+v", warmMemory)
	}

	restarted := newClient()
	warmRedis, err := measureQualificationPhase(traffic, func() error {
		derived, err := restarted.DerivedMetadata(ctx, detail)
		if err == nil && !ggufmeta.DerivedCoreReady(derived) {
			return fmt.Errorf("warm-redis derived metadata incomplete")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if warmRedis.rangeRequests != 0 || warmRedis.rangeBytes != 0 {
		t.Fatalf("fresh-client warm L2 unexpectedly reached origin: %+v", warmRedis)
	}
	if warmRedis.duration*5 >= cold.duration {
		t.Fatalf("fresh-client warm L2 was not at least 5x faster than cold authoritative lookup: cold=%s redis=%s", cold.duration, warmRedis.duration)
	}

	concurrent := newClient()
	const callers = 16
	concurrentWarmRedis, err := measureQualificationPhase(traffic, func() error {
		start := make(chan struct{})
		errs := make(chan error, callers)
		var wg sync.WaitGroup
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				derived, err := concurrent.DerivedMetadata(ctx, detail)
				if err == nil && !ggufmeta.DerivedCoreReady(derived) {
					err = fmt.Errorf("concurrent derived metadata incomplete")
				}
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if concurrentWarmRedis.rangeRequests != 0 || concurrentWarmRedis.rangeBytes != 0 {
		t.Fatalf("concurrent warm L2 unexpectedly reached origin: %+v", concurrentWarmRedis)
	}

	t.Logf("live Redis qualification repo=%s revision=%s candidate=%s", detail.ID, detail.Revision, candidates[0])
	t.Logf("cold_authoritative duration=%s range_requests=%d range_bytes=%d", cold.duration, cold.rangeRequests, cold.rangeBytes)
	t.Logf("warm_l1 duration=%s range_requests=%d range_bytes=%d", warmMemory.duration, warmMemory.rangeRequests, warmMemory.rangeBytes)
	t.Logf("warm_redis_fresh_client duration=%s range_requests=%d range_bytes=%d", warmRedis.duration, warmRedis.rangeRequests, warmRedis.rangeBytes)
	t.Logf("warm_redis_concurrent callers=%d total_duration=%s range_requests=%d range_bytes=%d", callers, concurrentWarmRedis.duration, concurrentWarmRedis.rangeRequests, concurrentWarmRedis.rangeBytes)
}
