package api

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

func TestRecommendationHandler(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	model := createModel(t, f, cookie)
	gib := int64(1024 * 1024 * 1024)
	handler := NewRecommendationHandler(f.auth, f.models, staticHardware{snapshot: hardware.Snapshot{
		RAMTotalBytes: 32 * gib, RAMAvailableBytes: 16 * gib,
		GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 12 * gib, FreeBytes: 10 * gib}},
	}})
	path := "/api/v1/models/" + model.ID + "/recommendation?context_length=2048"
	if w := doRequest(t, handler, http.MethodGet, path, nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", w.Code)
	}
	w := doRequest(t, handler, http.MethodGet, path, nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"context_length":2048`) || !strings.Contains(w.Body.String(), `"quantization"`) || !strings.Contains(w.Body.String(), `"CUDA0"`) {
		t.Fatalf("recommendation=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/recommendation?context_length=bad", nil, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("bad context=%d", w.Code)
	}
	if w := doRequest(t, handler, http.MethodGet, "/api/v1/models/missing/recommendation", nil, cookie); w.Code != http.StatusNotFound {
		t.Fatalf("missing=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodPost, path, nil, cookie); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", w.Code)
	}
}

func TestRecommendationReturnsEstimateWhenHardwareProbeFails(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	model := createModel(t, f, cookie)
	handler := NewRecommendationHandler(f.auth, f.models, staticHardware{err: errors.New("probe unavailable")})
	w := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/recommendation", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "probe unavailable") || !strings.Contains(w.Body.String(), `"confidence":"low"`) {
		t.Fatalf("hardware failure=%d body=%s", w.Code, w.Body.String())
	}
}

func TestModelInspectAndDetails(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	path := filepath.Join(f.dir, "metadata-Q4_K_M.gguf")
	writeAPIMetadataGGUF(t, path, "qwen2", 32768)
	model, err := f.models.Create(t.Context(), models.CreateModelInput{Name: "Metadata Model", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}

	inspect := NewModelInspectHandler(f.auth, f.models)
	if w := doRequest(t, inspect, http.MethodPost, "/api/v1/models/inspect", map[string]string{"gguf_path": path}, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("inspect auth=%d", w.Code)
	}
	w := doRequest(t, inspect, http.MethodPost, "/api/v1/models/inspect", map[string]string{"gguf_path": path}, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"architecture":"qwen2"`) || !strings.Contains(w.Body.String(), `"context_length":32768`) || !strings.Contains(w.Body.String(), `"has_mtp":false`) {
		t.Fatalf("inspect=%d %s", w.Code, w.Body.String())
	}
	if w := doRequest(t, inspect, http.MethodGet, "/api/v1/models/inspect", nil, cookie); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("inspect method=%d", w.Code)
	}
	bad := doRequest(t, inspect, http.MethodPost, "/api/v1/models/inspect", map[string]string{"gguf_path": "missing.gguf"}, cookie)
	if bad.Code != http.StatusOK || !strings.Contains(bad.Body.String(), `"warning"`) {
		t.Fatalf("inspect fallback=%d %s", bad.Code, bad.Body.String())
	}

	details := NewModelDetailsHandler(f.auth, f.models)
	w = doRequest(t, details, http.MethodGet, "/api/v1/models/"+model.ID+"/details?q=architecture&limit=10", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"metadata_total":1`) || !strings.Contains(w.Body.String(), `"key":"general.architecture"`) || !strings.Contains(w.Body.String(), `"has_mtp":false`) || strings.Contains(w.Body.String(), `qwen2.context_length`) {
		t.Fatalf("details=%d %s", w.Code, w.Body.String())
	}
	if w := doRequest(t, details, http.MethodGet, "/api/v1/models/"+model.ID+"/details?offset=-1", nil, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("bad page=%d", w.Code)
	}
	if w := doRequest(t, details, http.MethodGet, "/api/v1/models/missing/details", nil, cookie); w.Code != http.StatusNotFound {
		t.Fatalf("missing=%d", w.Code)
	}
	if w := doRequest(t, details, http.MethodPost, "/api/v1/models/"+model.ID+"/details", nil, cookie); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("details method=%d", w.Code)
	}
}

func TestModelDetailsWarnsForMalformedRegisteredGGUF(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	path := filepath.Join(f.dir, "malformed.gguf")
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := f.models.Create(t.Context(), models.CreateModelInput{Name: "Malformed", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewModelDetailsHandler(f.auth, f.models)
	w := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/details", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"warnings"`) || !strings.Contains(w.Body.String(), `"metadata":[]`) {
		t.Fatalf("malformed details=%d %s", w.Code, w.Body.String())
	}
	if offset, limit, ok := metadataPage(httptestRequest("?limit=9999")); !ok || offset != 0 || limit != 500 {
		t.Fatalf("page clamp=%d %d %v", offset, limit, ok)
	}
}

func httptestRequest(query string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://example.test/details"+query, nil)
	return r
}

func writeAPIMetadataGGUF(t *testing.T, path, architecture string, contextLength int64) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("GGUF")
	apiBinaryWrite(t, &b, uint32(3))
	apiBinaryWrite(t, &b, uint64(0))
	apiBinaryWrite(t, &b, uint64(3))
	apiString(t, &b, "general.architecture")
	apiBinaryWrite(t, &b, uint32(8))
	apiString(t, &b, architecture)
	apiString(t, &b, architecture+".context_length")
	apiBinaryWrite(t, &b, uint32(11))
	apiBinaryWrite(t, &b, contextLength)
	apiString(t, &b, "vendor.future.key")
	apiBinaryWrite(t, &b, uint32(8))
	apiString(t, &b, "visible")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func apiString(t *testing.T, b *bytes.Buffer, value string) {
	t.Helper()
	apiBinaryWrite(t, b, uint64(len(value)))
	_, _ = b.WriteString(value)
}

func apiBinaryWrite(t *testing.T, b *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(b, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
