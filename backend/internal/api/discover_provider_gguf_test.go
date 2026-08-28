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
	handler := NewDiscoverRecommendationHandler(fixture.auth, hf, staticHardware{snapshot: hardware.Snapshot{
		RAMAvailableBytes: 64 << 30,
		RAMTotalBytes: 64 << 30,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 24 << 30, TotalBytes: 24 << 30}},
	}}, managerSettings)

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
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, `"tier":"Unknown profile"`) {
		t.Fatalf("provider-backed mixed profile remained unknown: %s", body)
	}
}
