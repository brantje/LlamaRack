package api

import (
	"context"
	"sync"

	"github.com/brantje/llamarack/backend/internal/supervisor"
	"github.com/brantje/llamarack/backend/internal/telemetry"
)

type runtimeTelemetrySample struct {
	telemetry.Sample
	LlamaMetrics *telemetry.LlamaMetrics `json:"llama_metrics,omitempty"`
}

type endpointResolver func(string) (string, bool)

func attachLlamaMetrics(ctx context.Context, samples []telemetry.Sample, runtimes []supervisor.Runtime, resolve endpointResolver) []runtimeTelemetrySample {
	result := make([]runtimeTelemetrySample, len(samples))
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
			metrics, err := telemetry.FetchLlamaMetrics(ctx, endpoint)
			if err == nil {
				result[index].LlamaMetrics = metrics
			}
		}(index, endpoint)
	}
	wg.Wait()
	return result
}
