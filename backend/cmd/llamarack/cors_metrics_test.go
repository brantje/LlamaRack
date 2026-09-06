package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDynamicCORSExposesInferenceMetricHeaders(t *testing.T) {
	network, _ := testCORSNetwork(t)
	h := dynamicCORS(network, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-LlamaRack-Request-ID", "lr_test")
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodPost, "http://manager.local/v1/chat/completions", nil)
	r.Host = "manager.local"
	r.Header.Set("Origin", "https://manager.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	exposed := w.Header().Get("Access-Control-Expose-Headers")
	for _, expected := range []string{
		"X-LlamaRack-Request-ID",
		"X-LlamaRack-Instance",
		"X-LlamaRack-Queue-MS",
		"X-LlamaRack-TTFT-MS",
		"X-LiteLLM-Trace-ID",
	} {
		if !strings.Contains(exposed, expected) {
			t.Fatalf("missing exposed header %s in %q", expected, exposed)
		}
	}
	if strings.Contains(exposed, "X-LlamaCPP-Manager-") {
		t.Fatalf("previous product headers still exposed: %q", exposed)
	}
	if strings.Contains(strings.ToLower(exposed), "upstream-port") {
		t.Fatalf("worker port still exposed: %q", exposed)
	}
}
