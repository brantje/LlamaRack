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
		w.Header().Set("X-LlamaCPP-Manager-Request-ID", "lcm_test")
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
		"X-LlamaCPP-Manager-Request-ID",
		"X-LlamaCPP-Manager-Instance",
		"X-LlamaCPP-Manager-Queue-MS",
		"X-LlamaCPP-Manager-TTFT-MS",
		"X-LlamaCPP-Manager-Prompt-Tokens-Per-Second",
		"X-LlamaCPP-Manager-Generation-Tokens-Per-Second",
	} {
		if !strings.Contains(exposed, expected) {
			t.Fatalf("missing exposed header %s in %q", expected, exposed)
		}
	}
}
