package main

import "os"

const managedLlamaMetricsEnv = "LLAMA_ARG_ENDPOINT_METRICS"
const managedLlamaSlotsEnv = "LLAMA_ARG_ENDPOINT_SLOTS"

func enableManagedLlamaMetrics() {
	_ = os.Setenv(managedLlamaMetricsEnv, "1")
}

func enableManagedLlamaSlots() {
	_ = os.Setenv(managedLlamaSlotsEnv, "1")
}

func init() {
	enableManagedLlamaMetrics()
	enableManagedLlamaSlots()
}
