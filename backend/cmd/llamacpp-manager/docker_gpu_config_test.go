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

	assertFileContains := func(path string, values ...string) {
		t.Helper()
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(content)
		for _, value := range values {
			if !strings.Contains(text, value) {
				t.Errorf("%s missing %q", path, value)
			}
		}
	}

	assertFileContains(filepath.Join(backendRoot, "Dockerfile"),
		"NVIDIA_VISIBLE_DEVICES=all",
		"NVIDIA_DRIVER_CAPABILITIES=compute,utility",
	)
	assertFileContains(filepath.Join(repoRoot, "docker-compose.yml"),
		"NVIDIA_VISIBLE_DEVICES: ${NVIDIA_VISIBLE_DEVICES:-all}",
		"NVIDIA_DRIVER_CAPABILITIES: ${NVIDIA_DRIVER_CAPABILITIES:-compute,utility}",
	)
	assertFileContains(filepath.Join(repoRoot, "docker-compose.nvidia.yml"),
		"driver: nvidia",
		"count: all",
		"capabilities: [gpu]",
	)
}
