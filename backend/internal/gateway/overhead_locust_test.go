package gateway

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type gatewayLoadResult struct {
	Requests    int64
	Errors      int64
	Elapsed     time.Duration
	P95         time.Duration
	QueueP95    time.Duration
	OverheadP95 time.Duration
}

func TestGatewayOverheadLocust(t *testing.T) {
	if os.Getenv("LLAMARACK_GATEWAY_BENCH") != "1" {
		t.Skip("set LLAMARACK_GATEWAY_BENCH=1 to run the gateway load harness")
	}

	users := envInt("LLAMARACK_BENCH_USERS", 1000)
	warmup := envDuration("LLAMARACK_BENCH_WARMUP", 15*time.Second)
	duration := envDuration("LLAMARACK_BENCH_DURATION", 60*time.Second)
	if users <= 0 || warmup < 0 || duration <= 0 {
		t.Fatal("benchmark users/durations must be positive")
	}

	fixture := newGatewayFixture(t, true)
	endpoint, err := fixture.lifecycle.EnsureReady(context.Background(), fixture.instanceID)
	if err != nil {
		t.Fatal(err)
	}

	payload := `{"model":"gateway-model","messages":[{"role":"user","content":"benchmark"}],"padding":"` + strings.Repeat("x", 16<<10) + `"}`
	directURL := strings.TrimRight(endpoint, "/") + "/v1/chat/completions"
	noObs := httptest.NewServer(New(fixture.gateway.auth, nil, fixture.lifecycle))
	t.Cleanup(noObs.Close)
	full := httptest.NewServer(WithRequestLogContext(fixture.gateway, fixture.observability))
	t.Cleanup(full.Close)

	if warmup > 0 {
		_ = runGatewayLoad(t, full.URL+"/v1/chat/completions", fixture.secret, payload, users, warmup, false)
	}

	direct := runGatewayLoad(t, directURL, "", payload, users, duration, true)
	withoutObservability := runGatewayLoad(t, noObs.URL+"/v1/chat/completions", fixture.secret, payload, users, duration, true)
	withObservability := runGatewayLoad(t, full.URL+"/v1/chat/completions", fixture.secret, payload, users, duration, true)

	logGatewayLoadResult(t, "direct", direct)
	logGatewayLoadResult(t, "gateway-no-observability", withoutObservability)
	logGatewayLoadResult(t, "gateway-observability", withObservability)
}

func runGatewayLoad(t *testing.T, target, secret, payload string, users int, duration time.Duration, collect bool) gatewayLoadResult {
	t.Helper()
	transport := &http.Transport{
		MaxIdleConns:        users * 2,
		MaxIdleConnsPerHost: users,
		IdleConnTimeout:     30 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	defer transport.CloseIdleConnections()

	var requests atomic.Int64
	var failures atomic.Int64
	var mu sync.Mutex
	latencies := make([]time.Duration, 0, users*16)
	queues := make([]time.Duration, 0, users*16)
	overheads := make([]time.Duration, 0, users*16)
	start := time.Now()
	deadline := start.Add(duration)
	var wg sync.WaitGroup
	wg.Add(users)
	for worker := 0; worker < users; worker++ {
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				began := time.Now()
				req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(payload))
				if err == nil {
					req.Header.Set("Content-Type", "application/json")
					if secret != "" {
						req.Header.Set("Authorization", "Bearer "+secret)
					}
					resp, doErr := client.Do(req)
					if doErr != nil {
						err = doErr
					} else {
						_, _ = io.Copy(io.Discard, resp.Body)
						_ = resp.Body.Close()
						if resp.StatusCode < 200 || resp.StatusCode >= 400 {
							err = fmt.Errorf("status %d", resp.StatusCode)
						}
						if collect {
							mu.Lock()
							if queueMS, parseErr := strconv.ParseFloat(resp.Header.Get(headerQueueMS), 64); parseErr == nil {
								queues = append(queues, time.Duration(queueMS*float64(time.Millisecond)))
							}
							if overheadMS, parseErr := strconv.ParseFloat(resp.Header.Get(headerOverheadMS), 64); parseErr == nil {
								overheads = append(overheads, time.Duration(overheadMS*float64(time.Millisecond)))
							}
							mu.Unlock()
						}
					}
				}
				requests.Add(1)
				if err != nil {
					failures.Add(1)
				}
				if collect {
					mu.Lock()
					latencies = append(latencies, time.Since(began))
					mu.Unlock()
				}
				if remaining := time.Until(deadline); remaining > 0 {
					think := 500*time.Millisecond + time.Duration(rand.Int64N(int64(500*time.Millisecond)))
					if think > remaining {
						think = remaining
					}
					time.Sleep(think)
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	return gatewayLoadResult{
		Requests:    requests.Load(),
		Errors:      failures.Load(),
		Elapsed:     elapsed,
		P95:         durationP95(latencies),
		QueueP95:    durationP95(queues),
		OverheadP95: durationP95(overheads),
	}
}

func logGatewayLoadResult(t *testing.T, name string, result gatewayLoadResult) {
	t.Helper()
	rps := float64(result.Requests) / result.Elapsed.Seconds()
	t.Logf("%s users load: requests=%d errors=%d elapsed=%s rps=%.1f p95=%s queue_p95=%s overhead_p95=%s", name, result.Requests, result.Errors, result.Elapsed.Round(time.Millisecond), rps, result.P95, result.QueueP95, result.OverheadP95)
}

func durationP95(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]time.Duration(nil), values...)
	sort.Slice(copyValues, func(i, j int) bool { return copyValues[i] < copyValues[j] })
	index := (95*len(copyValues) + 99) / 100
	if index < 1 {
		index = 1
	}
	return copyValues[index-1]
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
