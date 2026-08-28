package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

func TestDiscoverRecommendationUsesHubGGUFMetadataForMixedProfile(t *testing.T) {
	payload := apiDiscoveryGGUF(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/empero-ai/Qwen3.8-27B-Ridge-GGUF":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "empero-ai/Qwen3.8-27B-Ridge-GGUF",
				"sha": "ridge-rev",
				"gguf": map[string]any{
					"total": int64(27_315_000_000),
					"architecture": "qwen35",
					"context_length": int64(262144),
				},
				"siblings": []map[string]any{{
					"rfilename": "Qwen3.8-27B-Ridge-3.7bpw.gguf",
					"size": int64(12_599_187_008),
				}},
			})
		case "/empero-ai/Qwen3.8-27B-Ridge-GGUF/resolve/ridge-rev/Qwen3.8-27B-Ridge-3.7bpw.gguf":
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	fixture := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, fixture)
	hf, err := huggingface.NewClientWithHTTP(provider.URL, nil, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	managerSettings := settings.New(fixture.models.DB(), settings.Defaults{SessionLifetime: time.Hour, StartupTimeout: time.Minute, AlwaysOnReconcile: time.Second})
	fullSnapshot := hardware.Snapshot{
		RAMAvailableBytes: 64 << 30,
		RAMTotalBytes: 64 << 30,
		RAMBandwidthBytesPerSecond: 52_000_000_000,
		GPUs: []hardware.GPU{{
			ID: "CUDA0", FreeBytes: 24 << 30, TotalBytes: 24 << 30,
			MemoryBandwidthBytesPerSecond: 288_032_000_000,
			PCIeBandwidthBytesPerSecond: 15_753_846_153,
		}},
	}
	handler := NewDiscoverRecommendationHandler(fixture.auth, hf, staticHardware{snapshot: fullSnapshot}, managerSettings)

	response := doRequest(t, handler, http.MethodGet, "/api/v1/huggingface/recommendations?repo=empero-ai%2FQwen3.8-27B-Ridge-GGUF", nil, cookie)
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, body)
	}
	for _, want := range []string{
		`"architecture":"qwen35"`,
		`"context_capability":262144`,
		`"name":"3.69BPW"`,
		`"tier":"Mixed quantization"`,
		`"recommended":true`,
		`"fit_label":"Fits on GPU"`,
		`"estimated_generation_speed":{"estimated":true`,
		`tok/s`,
		`288 GB/s theoretical VRAM bandwidth`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	for _, legacy := range []string{`"generation_speed"`, `"estimated_generation"`} {
		if strings.Contains(body, legacy) {
			t.Fatalf("legacy generation field %s leaked into API: %s", legacy, body)
		}
	}
	if strings.Contains(body, `"tier":"Unknown profile"`) || strings.Contains(body, `"speed":"Hardware-dependent"`) {
		t.Fatalf("provider-backed mixed profile retained generic guidance: %s", body)
	}

	hybridSnapshot := fullSnapshot
	hybridSnapshot.GPUs = []hardware.GPU{{
		ID: "CUDA0", FreeBytes: 8 << 30, TotalBytes: 8 << 30,
		MemoryBandwidthBytesPerSecond: 288_032_000_000,
		PCIeBandwidthBytesPerSecond: 15_753_846_153,
	}}
	hybridHandler := NewDiscoverRecommendationHandler(fixture.auth, hf, staticHardware{snapshot: hybridSnapshot}, managerSettings)
	hybridResponse := doRequest(t, hybridHandler, http.MethodGet, "/api/v1/huggingface/recommendations?repo=empero-ai%2FQwen3.8-27B-Ridge-GGUF", nil, cookie)
	hybridBody := hybridResponse.Body.String()
	if hybridResponse.Code != http.StatusOK {
		t.Fatalf("hybrid status=%d body=%s", hybridResponse.Code, hybridBody)
	}
	for _, want := range []string{
		`"fit_label":"GPU + CPU"`,
		`"estimated_generation_speed":{"estimated":true`,
		`Hybrid bandwidth-limited generation/decode estimate`,
		`measured memory-copy throughput`,
		`theoretical PCIe link`,
	} {
		if !strings.Contains(hybridBody, want) {
			t.Fatalf("hybrid missing %s in %s", want, hybridBody)
		}
	}
	for _, legacy := range []string{`"generation_speed"`, `"estimated_generation"`} {
		if strings.Contains(hybridBody, legacy) {
			t.Fatalf("legacy generation field %s leaked into hybrid API: %s", legacy, hybridBody)
		}
	}
}
