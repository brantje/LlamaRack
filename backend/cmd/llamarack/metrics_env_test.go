package main

import (
	"os"
	"testing"
)

func TestEnableManagedLlamaMetrics(t *testing.T) {
	t.Setenv(managedLlamaMetricsEnv, "0")
	enableManagedLlamaMetrics()
	if got := os.Getenv(managedLlamaMetricsEnv); got != "1" {
		t.Fatalf("%s=%q", managedLlamaMetricsEnv, got)
	}
}
