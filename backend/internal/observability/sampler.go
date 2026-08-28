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
	lifecycle *lifecycle.Service
	service   *Service
	interval  time.Duration
	persist   time.Duration

	mu     sync.RWMutex
	latest LiveSnapshot
	subs   map[chan LiveSnapshot]struct{}
}

func NewSampler(lifecycleService *lifecycle.Service, service *Service) *Sampler {
	return &Sampler{
		lifecycle: lifecycleService,
		service: service,
		interval: defaultLiveSampleInterval,
		persist: defaultPersistInterval,
		subs: map[chan LiveSnapshot]struct{}{},
	}
}

func (s *Sampler) Latest() LiveSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return copyLiveSnapshot(s.latest)
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
	runtimes, events, cancel, err := s.lifecycle.SubscribeRuntimes(ctx)
	if err != nil { return }
	defer cancel()
	current := make(map[string]supervisor.Runtime, len(runtimes))
	for _, runtime := range runtimes { current[runtime.InstanceID] = runtime }

	var currentHardware hardware.Snapshot
	collector := telemetry.New(func(context.Context) (hardware.Snapshot, error) { return currentHardware, nil })
	lastPersist := time.Time{}
	collect := func() {
		snapshot, err := s.lifecycle.HardwareSnapshot(ctx)
		if err != nil { return }
		currentHardware = snapshot
		values := make([]supervisor.Runtime, 0, len(current))
		for _, runtime := range current { values = append(values, runtime) }
		samples := collector.Collect(ctx, values)
		samples = applyHardwareFallback(samples, snapshot)
		withMetrics := attachNativeMetrics(ctx, samples, values, s.lifecycle.RuntimeEndpoint)
		gateway, _ := s.service.Summary(ctx, time.Now().Add(-15*time.Minute).UnixMilli())
		requests, _ := s.service.ListRequests(ctx, RequestFilters{SinceMS: time.Now().Add(-15*time.Minute).UnixMilli(), Limit: 20})
		live := LiveSnapshot{Type: "observability", CollectedAt: snapshot.CollectedAt, Hardware: snapshot, Telemetry: withMetrics, Gateway: gateway, Requests: requests}
		s.publish(live)

		plain := make([]telemetry.Sample, len(withMetrics))
		for index := range withMetrics { plain[index] = withMetrics[index].Sample }
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
		case <-ctx.Done(): return
		case runtime, ok := <-events:
			if !ok { return }
			current[runtime.InstanceID] = runtime
		case <-ticker.C:
			collect()
		}
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
	if len(samples) == 0 || len(snapshot.GPUs) == 0 { return samples }
	byID := make(map[string]hardware.GPU, len(snapshot.GPUs))
	for _, gpu := range snapshot.GPUs { byID[gpu.ID] = gpu }
	for index := range samples {
		gpus := snapshot.GPUs
		if len(samples[index].GPUDevices) > 0 {
			selected := make([]hardware.GPU, 0, len(samples[index].GPUDevices))
			for _, id := range samples[index].GPUDevices { if gpu, ok := byID[id]; ok { selected = append(selected, gpu) } }
			if len(selected) > 0 { gpus = selected }
		}
		if samples[index].GPUUtilizationPct == nil {
			var utilization float64
			for _, gpu := range gpus { utilization += gpu.UtilizationPct }
			utilization /= float64(len(gpus))
			samples[index].GPUUtilizationPct = &utilization
		}
		if samples[index].VRAMUsedBytes == nil {
			var used int64
			for _, gpu := range gpus { used += gpu.UsedBytes }
			samples[index].VRAMUsedBytes = &used
		}
	}
	return samples
}

type runtimeEndpointResolver func(string) (string, bool)

func attachNativeMetrics(ctx context.Context, samples []telemetry.Sample, runtimes []supervisor.Runtime, resolve runtimeEndpointResolver) []RuntimeTelemetrySample {
	result := make([]RuntimeTelemetrySample, len(samples))
	current := make(map[string]supervisor.Runtime, len(runtimes))
	for _, runtime := range runtimes { current[runtime.InstanceID] = runtime }
	var wg sync.WaitGroup
	for index := range samples {
		result[index].Sample = samples[index]
		runtime, ok := current[samples[index].InstanceID]
		if !ok || runtime.State != supervisor.Ready || runtime.PID != samples[index].PID || resolve == nil { continue }
		endpoint, ok := resolve(samples[index].InstanceID)
		if !ok || endpoint == "" { continue }
		wg.Add(1)
		go func(index int, endpoint string) {
			defer wg.Done()
			requestCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
			defer cancel()
			metrics, err := telemetry.FetchLlamaMetrics(requestCtx, endpoint)
			if err == nil { result[index].LlamaMetrics = metrics }
		}(index, endpoint)
	}
	wg.Wait()
	return result
}
