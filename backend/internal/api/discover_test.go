package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

func TestDiscoverRecommendationHandler(t *testing.T) {
	payload := apiDiscoveryGGUF(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/acme/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "acme/demo", "sha": "rev1", "siblings": []map[string]any{{"rfilename": "demo-Q4_K_M.gguf", "size": len(payload)}},
			})
		case "/acme/demo/resolve/rev1/demo-Q4_K_M.gguf":
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	hf, err := huggingface.NewClientWithHTTP(provider.URL, nil, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	managerSettings := settings.New(f.models.DB(), settings.Defaults{SessionLifetime: time.Hour, StartupTimeout: time.Minute, AlwaysOnReconcile: time.Second})
	handler := NewDiscoverRecommendationHandler(f.auth, hf, staticHardware{snapshot: hardware.Snapshot{
		RAMAvailableBytes: 16 << 30, RAMTotalBytes: 32 << 30,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 << 30, TotalBytes: 8 << 30}},
	}}, managerSettings)

	if got := doRequest(t, handler, http.MethodPost, "/api/v1/huggingface/recommendations?repo=acme%2Fdemo", nil, cookie).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", got)
	}
	if got := doRequest(t, handler, http.MethodGet, "/api/v1/huggingface/recommendations?repo=acme%2Fdemo", nil, nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized=%d", got)
	}
	if got := doRequest(t, handler, http.MethodGet, "/api/v1/huggingface/recommendations", nil, cookie).Code; got != http.StatusBadRequest {
		t.Fatalf("missing repo=%d", got)
	}
	if got := doRequest(t, handler, http.MethodGet, "/api/v1/huggingface/recommendations?repo=acme%2Fdemo&context_length=nope", nil, cookie).Code; got != http.StatusBadRequest {
		t.Fatalf("invalid context=%d", got)
	}

	w := doRequest(t, handler, http.MethodGet, "/api/v1/huggingface/recommendations?repo=acme%2Fdemo&context_length=8192", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"context_length":8192`) || !strings.Contains(w.Body.String(), `"artifact_id"`) || !strings.Contains(w.Body.String(), `"fit_label":"Fits on GPU"`) {
		t.Fatalf("recommendations=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Count(w.Body.String(), `"recommended":true`) != 1 {
		t.Fatalf("expected exactly one recommendation: %s", w.Body.String())
	}

	failedHardware := NewDiscoverRecommendationHandler(f.auth, hf, staticHardware{err: errors.New("probe failed")}, managerSettings)
	w = doRequest(t, failedHardware, http.MethodGet, "/api/v1/huggingface/recommendations?repo=acme%2Fdemo", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"hardware_available":false`) || !strings.Contains(w.Body.String(), `"hardware_warning":"probe failed"`) {
		t.Fatalf("hardware failure=%d body=%s", w.Code, w.Body.String())
	}

	w = doRequest(t, handler, http.MethodGet, "/api/v1/huggingface/recommendations?repo=missing%2Frepo", nil, cookie)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("provider failure=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDiscoverSettingsHandler(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	managerSettings := settings.New(f.models.DB(), settings.Defaults{SessionLifetime: time.Hour, StartupTimeout: time.Minute, AlwaysOnReconcile: time.Second})
	handler := NewDiscoverSettingsHandler(f.auth, managerSettings)

	if got := doRequest(t, handler, http.MethodGet, "/api/v1/settings/discover", nil, nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("unauthorized=%d", got)
	}
	w := doRequest(t, handler, http.MethodGet, "/api/v1/settings/discover", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"value":true`) || !strings.Contains(w.Body.String(), `"source":"default"`) {
		t.Fatalf("default=%d body=%s", w.Code, w.Body.String())
	}
	if got := doRequest(t, handler, http.MethodPut, "/api/v1/settings/discover", map[string]any{}, cookie).Code; got != http.StatusBadRequest {
		t.Fatalf("missing setting=%d", got)
	}
	if got := doRequest(t, handler, http.MethodPut, "/api/v1/settings/discover", []byte(`{"hybrid_recommendations_enabled":false,"extra":true}`), cookie).Code; got != http.StatusBadRequest {
		t.Fatalf("invalid body=%d", got)
	}
	w = doRequest(t, handler, http.MethodPut, "/api/v1/settings/discover", map[string]any{"hybrid_recommendations_enabled": false}, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"value":false`) || !strings.Contains(w.Body.String(), `"source":"database"`) {
		t.Fatalf("save=%d body=%s", w.Code, w.Body.String())
	}
	if got := doRequest(t, handler, http.MethodDelete, "/api/v1/settings/discover", nil, cookie).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", got)
	}
}

func apiDiscoveryGGUF(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	apiWriteBinary(t, &b, uint32(3))
	apiWriteBinary(t, &b, uint64(0))
	apiWriteBinary(t, &b, uint64(7))
	apiWriteString(t, &b, "general.architecture")
	apiWriteBinary(t, &b, uint32(8))
	apiWriteString(t, &b, "llama")
	for _, item := range []struct { key string; value int64 }{
		{"llama.context_length", 131072}, {"llama.block_count", 32}, {"llama.embedding_length", 4096}, {"llama.attention.head_count", 32}, {"llama.attention.head_count_kv", 8},
	} {
		apiWriteString(t, &b, item.key)
		apiWriteBinary(t, &b, uint32(11))
		apiWriteBinary(t, &b, item.value)
	}
	apiWriteString(t, &b, "tokenizer.ggml.tokens")
	return b.Bytes()
}

func apiWriteString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	apiWriteBinary(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}
func apiWriteBinary(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil { t.Fatal(err) }
}

var _ = context.Background
