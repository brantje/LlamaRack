package observability

import (
	"context"
	"sync"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
	"github.com/brantje/llamacpp-manager/backend/internal/telemetry"
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

type Sampler struct {
	lifecycle      *lifecycle.Service
	service        *Service
	interval       time.Duration
	persist        time.Duration
	gatewayRefresh time.Duration

	mu     sync.RWMutex
	latest LiveSnapshot
	states map[string]string
	subs   map[chan LiveSnapshot]struct{}
}

func NewSampler(lifecycleService *lifecycle.Service, service *Service, _ ...time.Duration) *Sampler {
	sampler := &Sampler{
		lifecycle:      lifecycleService,
		service:        service,
		interval:       defaultLiveSampleInterval,
		persist:        defaultPersistInterval,
		gatewayRefresh: defaultGatewayRefresh,
		states:         map[string]string{},
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
		live := LiveSnapshot{Type: "observability", CollectedAt: snapshot.CollectedAt, Hardware: snapshot, Telemetry: withMetrics, Gateway: gateway, Requests: requests}
		s.publish(live)

		plain := make([]telemetry.Sample, len(withMetrics))
		for index := range withMetrics {
			plain[index] = withMetrics[index].Sample
		}
		s.service.SetLatestHardware(snapshot, plain)
		if lastPersist.IsZero() || time.Since(lastPersist) >= s.persist {
			persistCtx, persistCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = s.service.RecordHardware(persistCtx, snapshot, plain)
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
			s.observeLifecycle(ctx, current[runtime.InstanceID], runtime)
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

func (s *Sampler) observeLifecycle(ctx context.Context, previous, next supervisor.Runtime) {
	if next.InstanceID == "" || previous.State == next.State || s.lifecycle == nil || s.service == nil {
		return
	}
	state := s.lifecycle.OperationalState(next.InstanceID)
	for _, transition := range classifyLifecycle(previous, next, state) {
		switch transition.Event {
		case LifecycleAutoload:
			s.lifecycle.AddManagerLog(next.InstanceID, "autoload triggered by inference request")
		case LifecycleLoad:
			s.lifecycle.AddManagerLog(next.InstanceID, "worker ready")
		case LifecycleFailedStart:
			message := "worker failed to start"
			if next.LastError != "" {
				message += ": " + next.LastError
			}
			s.lifecycle.AddManagerLog(next.InstanceID, message)
		}
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_ = s.service.RecordLifecycle(persistCtx, transition.Event, next.InstanceID, transition.Duration)
		cancel()
	}
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
			for _, gpu := range gpus {
				utilization += gpu.UtilizationPct
			}
			utilization /= float64(len(gpus))
			samples[index].GPUUtilizationPct = &utilization
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
