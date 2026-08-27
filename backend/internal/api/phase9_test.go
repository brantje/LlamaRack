package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
)

func TestPhase9RecommendationHandler(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	model := createModel(t, f, cookie)
	gib := int64(1024 * 1024 * 1024)
	handler := NewPhase9RecommendationHandler(f.auth, f.models, staticHardware{snapshot: hardware.Snapshot{
		RAMTotalBytes: 32*gib, RAMAvailableBytes: 16*gib,
		GPUs: []hardware.GPU{{ID:"CUDA0", TotalBytes:12*gib, FreeBytes:10*gib}},
	}})
	path := "/api/v1/models/"+model.ID+"/recommendation?context_length=2048"
	if w := doRequest(t, handler, http.MethodGet, path, nil, nil); w.Code != http.StatusUnauthorized { t.Fatalf("unauthenticated=%d", w.Code) }
	w := doRequest(t, handler, http.MethodGet, path, nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"context_length":2048`) || !strings.Contains(w.Body.String(), `"quantization"`) || !strings.Contains(w.Body.String(), `"CUDA0"`) {
		t.Fatalf("recommendation=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/recommendation?context_length=bad", nil, cookie); w.Code != http.StatusBadRequest { t.Fatalf("bad context=%d", w.Code) }
	if w := doRequest(t, handler, http.MethodGet, "/api/v1/models/missing/recommendation", nil, cookie); w.Code != http.StatusNotFound { t.Fatalf("missing=%d body=%s", w.Code, w.Body.String()) }
	if w := doRequest(t, handler, http.MethodPost, path, nil, cookie); w.Code != http.StatusMethodNotAllowed { t.Fatalf("method=%d", w.Code) }
}

func TestPhase9ReturnsEstimateWhenHardwareProbeFails(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	model := createModel(t, f, cookie)
	handler := NewPhase9RecommendationHandler(f.auth, f.models, staticHardware{err: errors.New("probe unavailable")})
	w := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/recommendation", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "probe unavailable") || !strings.Contains(w.Body.String(), `"confidence":"low"`) {
		t.Fatalf("hardware failure=%d body=%s", w.Code, w.Body.String())
	}
}
