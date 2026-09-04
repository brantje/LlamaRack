package observability

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
	"github.com/brantje/llamarack/backend/internal/supervisor"
	"github.com/brantje/llamarack/backend/internal/systemlog"
	"github.com/brantje/llamarack/backend/internal/telemetry"
)

const (
	defaultLiveSampleInterval = time.Second
	defaultPersistInterval    = 10 * time.Second
	defaultGatewayRefresh     = 2 * time.Second
)

type RuntimeTelemetrySample struct {
	telemetry.Sample
	LlamaMetrics *telemetry.LlamaMetrics `json:"llama_metrics,omitempty"`
}

type LiveSnapshot struct {
	Type        string                   `json:"type,omitempty"`
	CollectedAt time.Time                `json:"collected_at"`
	Hardware    hardware.Snapshot        `json:"hardware"`
	Telemetry   []RuntimeTelemetrySample `json:"telemetry"`
	Gateway     Summary                  `json:"gateway"`
	Requests    []RequestRecord          `json:"requests"`
}

type throughputBaseline struct {
	PID              int
	PromptTokens     *float64
	PromptSeconds    *float64
	PredictedTokens  *float64
	PredictedSeconds *float64
}

type Sampler struct {
	lifecycle      *lifecycle.Service
	service        *Service
	interval       time.Duration
	persist        time.Duration
	gatewayRefresh time.Duration

	mu         sync.RWMutex
	latest     LiveSnapshot
	states     map[string]string
	throughput map[string]throughputBaseline
	subs       map[chan LiveSnapshot]struct{}
}

func NewSampler(lifecycleService *lifecycle.Service, service *Service, _ ...time.Duration) *Sampler {
	sampler := &Sampler{
		lifecycle:      lifecycleService,
		service:        service,
		interval:       defaultLiveSampleInterval,
		persist:        defaultPersistInterval,
		gatewayRefresh: defaultGatewayRefresh,
		states:         map[string]string{},
		throughput:     map[string]throughputBaseline{},
		subs:           map[chan LiveSnapshot]struct{}{},
	}
	if lifecycleService != nil && service != nil {
		lifecycleService.SetObservabilityRecorder(service.RecordLifecycle)
	}
	return sampler
}

func (s *Sampler) Latest() LiveSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyLiveSnapshot(s.latest)
}

func (s *Sampler) RuntimeStates() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.states))
	for instanceID, state := range s.states {
		out[instanceID] = state
	}
	return out
}

func (s *Sampler) Subscribe() (LiveSnapshot, <-chan LiveSnapshot, func()) {
	s.mu.Lock()
	snapshot := copyLiveSnapshot(s.latest)
	ch := make(chan LiveSnapshot, 4)
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subs, ch)
			close(ch)
			s.mu.Unlock()
		})
	}
	return snapshot, ch, cancel
}

func (s *Sampler) Run(ctx context.Context) {
	if s.lifecycle == nil || s.service == nil {
		return
	}
	runtimes, events, cancel, err := s.lifecycle.SubscribeRuntimes(ctx)
	if err != nil {
		return
	}
	defer cancel()
	current := make(map[string]supervisor.Runtime, len(runtimes))
	for _, runtime := range runtimes {
		current[runtime.InstanceID] = runtime
	}
	s.refreshRuntimeStates(ctx, current)

	var currentHardware hardware.Snapshot
	collector := telemetry.New(func(context.Context) (hardware.Snapshot, error) { return currentHardware, nil })
	lastPersist := time.Time{}
	lastGateway := time.Time{}
	gateway := Summary{}
	requests := []RequestRecord{}
	collect := func() {
		s.refreshRuntimeStates(ctx, current)
		snapshot, err := s.lifecycle.HardwareSnapshot(ctx)
		if err != nil {
			return
		}
		currentHardware = snapshot
		values := make([]supervisor.Runtime, 0, len(current))
		for _, runtime := range current {
			values = append(values, runtime)
		}
		samples := collector.Collect(ctx, values)
		samples = applyHardwareFallback(samples, snapshot)
		withMetrics := attachNativeMetrics(ctx, samples, values, s.lifecycle.RuntimeEndpoint)
		s.applyDerivedThroughput(withMetrics)
		now := time.Now()
		if lastGateway.IsZero() || now.Sub(lastGateway) >= s.gatewayRefresh {
			since := now.Add(-15 * time.Minute).UnixMilli()
			if value, summaryErr := s.service.Summary(ctx, since); summaryErr == nil {
				gateway = value
			}
			if value, requestErr := s.service.ListRequests(ctx, RequestFilters{SinceMS: since, Limit: 20}); requestErr == nil {
				requests = value
			}
			lastGateway = now
		}

		plain := make([]telemetry.Sample, len(withMetrics))
		for index := range withMetrics {
			plain[index] = withMetrics[index].Sample
		}
		s.service.SetLatestHardware(snapshot, plain)

		live := LiveSnapshot{Type: "observability", CollectedAt: snapshot.CollectedAt, Hardware: snapshot, Telemetry: withMetrics, Gateway: gateway, Requests: requests}
		s.publish(live)

		if lastPersist.IsZero() || time.Since(lastPersist) >= s.persist {
			persistCtx, persistCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = s.service.RecordHardware(persistCtx, snapshot, plain)
			_ = s.service.RecordContextMetrics(persistCtx, snapshot.CollectedAt, withMetrics)
			persistCancel()
			lastPersist = time.Now()
		}
	}

	collect()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case runtime, ok := <-events:
			if !ok {
				return
			}
			current[runtime.InstanceID] = runtime
			s.refreshRuntimeStates(ctx, current)
		case <-ticker.C:
			collect()
		}
	}
}

func (s *Sampler) refreshRuntimeStates(ctx context.Context, current map[string]supervisor.Runtime) {
	if s.lifecycle == nil {
		return
	}
	states := map[string]string{}
	if items, err := s.lifecycle.Instances().List(ctx); err == nil {
		for _, instance := range items {
			states[instance.ID] = string(supervisor.Unloaded)
		}
	}
	for instanceID, runtime := range current {
		if instanceID != "" {
			states[instanceID] = string(runtime.State)
		}
	}
	s.mu.Lock()
	s.states = states
	s.mu.Unlock()
}

// applyDerivedThroughput works around llama.cpp builds where the instantaneous
// prompt/prediction throughput gauges remain zero while inference is active.
// The cumulative token and processing-time counters remain monotonic, so their
// deltas provide the same average throughput for work completed between samples.
func (s *Sampler) applyDerivedThroughput(samples []RuntimeTelemetrySample) {
	seen := make(map[string]bool, len(samples))
	for index := range samples {
		instanceID := samples[index].InstanceID
		if instanceID == "" {
			continue
		}
		seen[instanceID] = true
		metrics := samples[index].LlamaMetrics
		if metrics == nil {
			continue
		}
		previous, ok := s.throughput[instanceID]
		if ok && previous.PID == samples[index].PID {
			if metrics.PromptTokensPerSecond == nil || *metrics.PromptTokensPerSecond <= 0 {
				metrics.PromptTokensPerSecond = counterRate(metrics.PromptTokensTotal, metrics.PromptSecondsTotal, previous.PromptTokens, previous.PromptSeconds)
			}
			if metrics.PredictedTokensPerSecond == nil || *metrics.PredictedTokensPerSecond <= 0 {
				metrics.PredictedTokensPerSecond = counterRate(metrics.PredictedTokensTotal, metrics.PredictedSecondsTotal, previous.PredictedTokens, previous.PredictedSeconds)
			}
		}
		s.throughput[instanceID] = throughputBaseline{
			PID:              samples[index].PID,
			PromptTokens:     copyMetricValue(metrics.PromptTokensTotal),
			PromptSeconds:    copyMetricValue(metrics.PromptSecondsTotal),
			PredictedTokens:  copyMetricValue(metrics.PredictedTokensTotal),
			PredictedSeconds: copyMetricValue(metrics.PredictedSecondsTotal),
		}
	}
	for instanceID := range s.throughput {
		if !seen[instanceID] {
			delete(s.throughput, instanceID)
		}
	}
}

func counterRate(currentTokens, currentSeconds, previousTokens, previousSeconds *float64) *float64 {
	if currentTokens == nil || currentSeconds == nil || previousTokens == nil || previousSeconds == nil {
		return nil
	}
	tokens := *currentTokens - *previousTokens
	seconds := *currentSeconds - *previousSeconds
	if tokens <= 0 || seconds <= 0 {
		return nil
	}
	value := tokens / seconds
	return &value
}

func copyMetricValue(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (s *Sampler) publish(snapshot LiveSnapshot) {
	s.mu.Lock()
	s.latest = copyLiveSnapshot(snapshot)
	for ch := range s.subs {
		select {
		case ch <- copyLiveSnapshot(snapshot):
		default:
		}
	}
	s.mu.Unlock()
}

func copyLiveSnapshot(value LiveSnapshot) LiveSnapshot {
	copyValue := value
	copyValue.Hardware.GPUs = append([]hardware.GPU(nil), value.Hardware.GPUs...)
	copyValue.Hardware.Processes = append([]hardware.GPUProcess(nil), value.Hardware.Processes...)
	copyValue.Telemetry = append([]RuntimeTelemetrySample(nil), value.Telemetry...)
	copyValue.Requests = append([]RequestRecord(nil), value.Requests...)
	return copyValue
}

func applyHardwareFallback(samples []telemetry.Sample, snapshot hardware.Snapshot) []telemetry.Sample {
	if len(samples) == 0 || len(snapshot.GPUs) == 0 {
		return samples
	}
	byID := make(map[string]hardware.GPU, len(snapshot.GPUs))
	for _, gpu := range snapshot.GPUs {
		byID[gpu.ID] = gpu
	}
	for index := range samples {
		gpus := snapshot.GPUs
		if len(samples[index].GPUDevices) > 0 {
			selected := make([]hardware.GPU, 0, len(samples[index].GPUDevices))
			for _, id := range samples[index].GPUDevices {
				if gpu, ok := byID[id]; ok {
					selected = append(selected, gpu)
				}
			}
			if len(selected) > 0 {
				gpus = selected
			}
		}
		if samples[index].GPUUtilizationPct == nil {
			var utilization float64
			deviceIDs := make([]string, 0, len(gpus))
			for _, gpu := range gpus {
				utilization += gpu.UtilizationPct
				deviceIDs = append(deviceIDs, gpu.ID)
			}
			utilization /= float64(len(gpus))
			samples[index].GPUUtilizationPct = &utilization
			systemlog.Log(systemlog.Debug, "telemetry", fmt.Sprintf("GPU util for %s unavailable, using %s device-wide (global fallback)", samples[index].InstanceID, strings.Join(deviceIDs, ",")))
		}
		if samples[index].VRAMUsedBytes == nil {
			var used int64
			for _, gpu := range gpus {
				used += gpu.UsedBytes
			}
			samples[index].VRAMUsedBytes = &used
		}
	}
	return samples
}

type runtimeEndpointResolver func(string) (string, bool)

func attachNativeMetrics(ctx context.Context, samples []telemetry.Sample, runtimes []supervisor.Runtime, resolve runtimeEndpointResolver) []RuntimeTelemetrySample {
	result := make([]RuntimeTelemetrySample, len(samples))
	current := make(map[string]supervisor.Runtime, len(runtimes))
	for _, runtime := range runtimes {
		current[runtime.InstanceID] = runtime
	}
	var wg sync.WaitGroup
	for index := range samples {
		result[index].Sample = samples[index]
		runtime, ok := current[samples[index].InstanceID]
		if !ok || runtime.State != supervisor.Ready || runtime.PID != samples[index].PID || resolve == nil {
			continue
		}
		endpoint, ok := resolve(samples[index].InstanceID)
		if !ok || endpoint == "" {
			continue
		}
		wg.Add(1)
		go func(index int, endpoint string) {
			defer wg.Done()
			requestCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
			defer cancel()
			metrics, err := telemetry.FetchLlamaMetrics(requestCtx, endpoint)
			if err == nil {
				result[index].LlamaMetrics = metrics
			}
		}(index, endpoint)
	}
	wg.Wait()
	return result
}
