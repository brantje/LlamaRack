package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/brantje/llamarack/backend/internal/benchmark"
	"github.com/brantje/llamarack/backend/internal/instances"
)

type fakeBenchmarkOverrideService struct {
	*fakeBenchmarkManagementService
	overrides benchmark.RuntimeOverrides
}

func (s *fakeBenchmarkOverrideService) CreateWithOverrides(_ context.Context, id string, workload *benchmark.WorkloadProfile, overrides benchmark.RuntimeOverrides) (benchmark.Run, error) {
	s.createdID, s.createdWork, s.overrides = id, workload, overrides
	return s.run, s.err
}

func TestBenchmarkCreateAcceptsTypedRuntimeOverrides(t *testing.T) {
	base := &fakeBenchmarkManagementService{run: benchmark.Run{ID: "run-1", Status: benchmark.StatusQueued}}
	service := &fakeBenchmarkOverrideService{fakeBenchmarkManagementService: base}
	resolver := fakeBenchmarkInstanceResolver{bySlug: map[string]instances.Instance{"coder": {ID: "instance-uuid", Slug: "coder"}}}
	mux := benchmarkTestMux(service, resolver)
	w := benchmarkRequest(t, mux, http.MethodPost, "/api/v1/instances/coder/benchmarks", map[string]any{
		"workload":          map[string]any{"prompt_tokens": []int{256}, "generation_tokens": []int{32}, "repetitions": 3, "warmup": false},
		"runtime_overrides": map[string]any{"batch_size": 1024, "flash_attention": true, "gpu_devices": []string{"CUDA1", "CUDA0"}, "tensor_split": "2,1"},
	}, true)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if service.overrides.BatchSize == nil || *service.overrides.BatchSize != 1024 || service.overrides.FlashAttention == nil || !*service.overrides.FlashAttention || service.overrides.GPUDevices == nil || len(*service.overrides.GPUDevices) != 2 {
		t.Fatalf("overrides=%+v", service.overrides)
	}
}

func TestBenchmarkCreateRejectsUnknownOrPathLikeOverrideFields(t *testing.T) {
	base := &fakeBenchmarkManagementService{run: benchmark.Run{ID: "run-1", Status: benchmark.StatusQueued}}
	service := &fakeBenchmarkOverrideService{fakeBenchmarkManagementService: base}
	resolver := fakeBenchmarkInstanceResolver{bySlug: map[string]instances.Instance{"coder": {ID: "instance-uuid", Slug: "coder"}}}
	mux := benchmarkTestMux(service, resolver)
	for _, runtime := range []map[string]any{
		{"raw_argv": []string{"--model", "/tmp/evil.gguf"}},
		{"model_path": "/tmp/evil.gguf"},
		{"helper_path": "/tmp/evil.gguf"},
		{"executable": "/bin/sh"},
		{"unknown_setting": true},
	} {
		w := benchmarkRequest(t, mux, http.MethodPost, "/api/v1/instances/coder/benchmarks", map[string]any{"runtime_overrides": runtime}, true)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("runtime=%v status=%d body=%s", runtime, w.Code, w.Body.String())
		}
	}
}
