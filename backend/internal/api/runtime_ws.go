package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/brantje/llamacpp-manager/backend/internal/auth"
	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/lifecycle"
	"github.com/brantje/llamacpp-manager/backend/internal/observability"
	"github.com/brantje/llamacpp-manager/backend/internal/supervisor"
	"github.com/brantje/llamacpp-manager/backend/internal/telemetry"
)

type runtimeWebSocketHandler struct {
	auth           *auth.Service
	lifecycle      *lifecycle.Service
	sampler        *observability.Sampler
	allowedOrigins string
}

type runtimeEvent struct {
	Type    string             `json:"type"`
	Runtime supervisor.Runtime `json:"runtime"`
}

type runtimeSnapshotEvent struct {
	Type     string               `json:"type"`
	Runtimes []supervisor.Runtime `json:"runtimes"`
}

type runtimeTelemetryEvent struct {
	Type      string                   `json:"type"`
	Telemetry []runtimeTelemetrySample `json:"telemetry"`
}

func NewRuntimeWebSocketHandler(a *auth.Service, l *lifecycle.Service, allowedOrigins string, samplers ...*observability.Sampler) http.Handler {
	var sampler *observability.Sampler
	if len(samplers) > 0 {
		sampler = samplers[0]
	}
	return &runtimeWebSocketHandler{auth: a, lifecycle: l, sampler: sampler, allowedOrigins: allowedOrigins}
}

func sharedTelemetry(samples []observability.RuntimeTelemetrySample) []runtimeTelemetrySample {
	out := make([]runtimeTelemetrySample, len(samples))
	for index := range samples {
		out[index] = runtimeTelemetrySample{Sample: samples[index].Sample, LlamaMetrics: samples[index].LlamaMetrics}
	}
	return out
}

func (h *runtimeWebSocketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, _, err := h.auth.ConsumeWebSocketTicket(r.Context(), r.URL.Query().Get("ticket")); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(request *http.Request) bool {
		return websocketOriginAllowed(request, h.allowedOrigins)
	}}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	snapshot, events, cancel, err := h.lifecycle.SubscribeRuntimes(r.Context())
	if err != nil {
		return
	}
	defer cancel()
	if err := conn.WriteJSON(runtimeSnapshotEvent{Type: "runtime_snapshot", Runtimes: snapshot}); err != nil {
		return
	}

	var observabilityEvents <-chan observability.LiveSnapshot
	if h.sampler != nil {
		initial, live, cancelLive := h.sampler.Subscribe()
		defer cancelLive()
		observabilityEvents = live
		if !initial.CollectedAt.IsZero() {
			if err := conn.WriteJSON(initial); err != nil {
				return
			}
			if len(initial.Telemetry) > 0 {
				if err := conn.WriteJSON(runtimeTelemetryEvent{Type: "runtime_telemetry", Telemetry: sharedTelemetry(initial.Telemetry)}); err != nil {
					return
				}
			}
		}
	}

	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-disconnected:
			return
		case runtime, open := <-events:
			if !open {
				return
			}
			if err := conn.WriteJSON(runtimeEvent{Type: "runtime", Runtime: runtime}); err != nil {
				return
			}
		case sample, open := <-observabilityEvents:
			if !open {
				observabilityEvents = nil
				continue
			}
			if err := conn.WriteJSON(sample); err != nil {
				return
			}
			if len(sample.Telemetry) > 0 {
				if err := conn.WriteJSON(runtimeTelemetryEvent{Type: "runtime_telemetry", Telemetry: sharedTelemetry(sample.Telemetry)}); err != nil {
					return
				}
			}
		}
	}
}

// applyGlobalTelemetryFallback is retained for the focused telemetry helper
// tests. Production WebSocket clients consume the shared observability sampler.
func applyGlobalTelemetryFallback(samples []telemetry.Sample, snapshot hardware.Snapshot) []telemetry.Sample {
	if len(samples) == 0 || len(snapshot.GPUs) == 0 {
		return samples
	}

	byID := make(map[string]hardware.GPU, len(snapshot.GPUs))
	for _, gpu := range snapshot.GPUs {
		byID[gpu.ID] = gpu
	}
	fallbackGPUs := func(sample telemetry.Sample) []hardware.GPU {
		if len(sample.GPUDevices) == 0 {
			return snapshot.GPUs
		}
		selected := make([]hardware.GPU, 0, len(sample.GPUDevices))
		for _, deviceID := range sample.GPUDevices {
			if gpu, ok := byID[deviceID]; ok {
				selected = append(selected, gpu)
			}
		}
		if len(selected) == 0 {
			return snapshot.GPUs
		}
		return selected
	}

	for index := range samples {
		gpus := fallbackGPUs(samples[index])
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

func runtimeValues(current map[string]supervisor.Runtime) []supervisor.Runtime {
	values := make([]supervisor.Runtime, 0, len(current))
	for _, runtime := range current {
		values = append(values, runtime)
	}
	return values
}

func hasRunningRuntime(runtimes []supervisor.Runtime) bool {
	for _, runtime := range runtimes {
		if runtime.PID > 0 {
			return true
		}
	}
	return false
}

func websocketOriginAllowed(r *http.Request, configured string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range strings.Split(configured, ",") {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Hostname() == "" || (originURL.Scheme != "http" && originURL.Scheme != "https") {
		return false
	}
	requestURL, err := url.Parse("http://" + r.Host)
	if err != nil || requestURL.Hostname() == "" {
		return false
	}
	return strings.EqualFold(originURL.Hostname(), requestURL.Hostname())
}
