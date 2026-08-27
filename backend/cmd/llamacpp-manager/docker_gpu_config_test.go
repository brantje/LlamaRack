package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDockerNVIDIATelemetryRuntimeConfiguration(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	backendRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	repoRoot := filepath.Dir(backendRoot)

	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(content)
	}
	assertContains := func(path, text string, values ...string) {
		t.Helper()
		for _, value := range values {
			if !strings.Contains(text, value) {
				t.Errorf("%s missing %q", path, value)
			}
		}
	}
	assertNotContains := func(path, text string, values ...string) {
		t.Helper()
		for _, value := range values {
			if strings.Contains(text, value) {
				t.Errorf("%s unexpectedly contains %q", path, value)
			}
		}
	}

	dockerfilePath := filepath.Join(backendRoot, "Dockerfile")
	baseComposePath := filepath.Join(repoRoot, "docker-compose.yml")
	nvidiaComposePath := filepath.Join(repoRoot, "docker-compose.nvidia.yml")
	dockerfile := read(dockerfilePath)
	baseCompose := read(baseComposePath)
	nvidiaCompose := read(nvidiaComposePath)

	// NVIDIA exposure must only be enabled together with the CUDA llama.cpp
	// image. Otherwise nvidia-smi can make the scheduler select CUDA0 while a
	// CPU-only llama-server rejects --device CUDA0.
	assertNotContains(dockerfilePath, dockerfile,
		"NVIDIA_VISIBLE_DEVICES=all",
		"NVIDIA_DRIVER_CAPABILITIES=compute,utility",
	)
	assertNotContains(baseComposePath, baseCompose,
		"NVIDIA_VISIBLE_DEVICES:",
		"NVIDIA_DRIVER_CAPABILITIES:",
	)
	assertContains(nvidiaComposePath, nvidiaCompose,
		"LLAMA_IMAGE: ${LLAMA_IMAGE:-ghcr.io/ggml-org/llama.cpp:server-cuda}",
		"NVIDIA_VISIBLE_DEVICES: ${NVIDIA_VISIBLE_DEVICES:-all}",
		"NVIDIA_DRIVER_CAPABILITIES: ${NVIDIA_DRIVER_CAPABILITIES:-compute,utility}",
		"driver: nvidia",
		"count: all",
		"capabilities: [gpu]",
	)
}
