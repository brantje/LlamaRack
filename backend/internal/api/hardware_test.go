package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/llamacpp"
	"github.com/brantje/llamacpp-manager/backend/internal/llamaconfig"
)

type staticHardware struct {
	snapshot hardware.Snapshot
	err      error
}

func (s staticHardware) Snapshot(context.Context) (hardware.Snapshot, error) {
	return s.snapshot, s.err
}

func llamaConfigProfile() (llamacpp.Profile, error) {
	return llamacpp.Profile{Path: "/app/llama-server", Version: "test", Options: []llamacpp.Option{
		{Key: "threads", ValueHint: "N", Kind: "integer"},
		{Key: "ctx-size", ValueHint: "N", Kind: "integer"},
	}}, nil
}

func TestHardwareHandler(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	handler := NewHardwareHandler(f.auth, staticHardware{snapshot: hardware.Snapshot{
		RAMTotalBytes: 1024, RAMAvailableBytes: 512,
		GPUs: []hardware.GPU{{ID: "CUDA0", Backend: "cuda", Name: "GPU", TotalBytes: 100, FreeBytes: 80}},
	}})

	if w := doRequest(t, handler, http.MethodGet, "/api/v1/hardware", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d body=%s", w.Code, w.Body.String())
	}
	w := doRequest(t, handler, http.MethodGet, "/api/v1/hardware", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"CUDA0"`) || !strings.Contains(w.Body.String(), `"ram_available_bytes":512`) {
		t.Fatalf("hardware=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodPost, "/api/v1/hardware", nil, cookie); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", w.Code)
	}

	failed := NewHardwareHandler(f.auth, staticHardware{err: errors.New("probe failed")})
	if w := doRequest(t, failed, http.MethodGet, "/api/v1/hardware", nil, cookie); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("probe failure=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLlamaConfigHandlerGlobalAndEffectiveValues(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	store := llamaconfig.New(f.models.DB())
	handler := NewLlamaConfigHandler(f.auth, store, llamaConfigProfile)

	if w := doRequest(t, handler, http.MethodGet, "/api/v1/llamacpp/config", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", w.Code)
	}
	w := doRequest(t, handler, http.MethodPut, "/api/v1/llamacpp/config", map[string]any{
		"options": map[string]string{"threads": "4", "ctx-size": "4096"},
	}, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"threads":"4"`) {
		t.Fatalf("put global=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, handler, http.MethodGet, "/api/v1/llamacpp/config", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"global"`) || !strings.Contains(w.Body.String(), `"threads":"global"`) {
		t.Fatalf("get global=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodPut, "/api/v1/llamacpp/config?model_id=m1", map[string]any{"options": map[string]string{}}, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("scoped put=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodPut, "/api/v1/llamacpp/config", map[string]any{"options": map[string]string{"unknown": "x"}}, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("unsupported option=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodPatch, "/api/v1/llamacpp/config", nil, cookie); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", w.Code)
	}
}

func TestLlamaConfigHandlerProfileAndLookupErrors(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	store := llamaconfig.New(f.models.DB())
	unavailable := NewLlamaConfigHandler(f.auth, store, func() (llamacpp.Profile, error) {
		return llamacpp.Profile{}, errors.New("binary unavailable")
	})
	if w := doRequest(t, unavailable, http.MethodGet, "/api/v1/llamacpp/config", nil, cookie); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("profile failure=%d body=%s", w.Code, w.Body.String())
	}

	handler := NewLlamaConfigHandler(f.auth, store, llamaConfigProfile)
	if w := doRequest(t, handler, http.MethodGet, "/api/v1/llamacpp/config?instance_id=missing", nil, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("missing instance=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodPut, "/api/v1/llamacpp/config", []byte(`{"options":{"threads":"4"},"extra":true}`), cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("decode error=%d body=%s", w.Code, w.Body.String())
	}
}
