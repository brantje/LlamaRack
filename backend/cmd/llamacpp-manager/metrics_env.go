package main

import "os"

const managedLlamaMetricsEnv = "LLAMA_ARG_ENDPOINT_METRICS"

func enableManagedLlamaMetrics() {
	_ = os.Setenv(managedLlamaMetricsEnv, "1")
}

func init() {
	enableManagedLlamaMetrics()
}
