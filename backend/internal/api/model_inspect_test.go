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

	"github.com/brantje/llamarack/backend/internal/hardware"
	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/llamacpp"
	"github.com/brantje/llamarack/backend/internal/models"
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
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"context_length":2048`) || !strings.Contains(w.Body.String(), `"quantization"`) || !strings.Contains(w.Body.String(), `"CUDA0"`) || !strings.Contains(w.Body.String(), `"placement_ranges"`) {
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


func TestRecommendationInstanceRuntimeParameters(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	model := createModel(t, f, cookie)
	enabled := true
	spill := true
	instance, err := f.server.lifecycle.Instances().Create(t.Context(), instances.CreateInput{
		ModelID: model.ID, Name: "Recommendation instance", Enabled: &enabled, SystemSpilloverEnabled: &spill,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewRecommendationHandler(f.auth, f.models, staticHardware{snapshot: hardware.Snapshot{RAMAvailableBytes: 16 << 30}})

	w := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/recommendation?instance_id="+instance.ID+"&system_spillover_enabled=false&gpu_mode=auto", nil, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("instance recommendation=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/recommendation?instance_id=missing", nil, cookie); w.Code != http.StatusNotFound {
		t.Fatalf("missing instance=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/recommendation?instance_id="+instance.ID+"&system_spillover_enabled=maybe", nil, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid spillover=%d body=%s", w.Code, w.Body.String())
	}
	if w := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/recommendation?instance_id="+instance.ID+"&gpu_mode=other", nil, cookie); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid gpu mode=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSplitRecommendationDevices(t *testing.T) {
	got := splitRecommendationDevices(" CUDA0,CUDA1,CUDA0, ")
	if len(got) != 2 || got[0] != "CUDA0" || got[1] != "CUDA1" {
		t.Fatalf("devices=%v", got)
	}
}


func TestRecommendationRuntimePreviewOverridesAndModelBinding(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	model := createModel(t, f, cookie)
	enabled := true
	spill := false
	instance, err := f.server.lifecycle.Instances().Create(t.Context(), instances.CreateInput{
		ModelID: model.ID, Name: "Runtime preview", Enabled: &enabled, SystemSpilloverEnabled: &spill,
		GPUMode: "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	gib := int64(1024 * 1024 * 1024)
	handler := NewRecommendationHandler(f.auth, f.models, staticHardware{snapshot: hardware.Snapshot{
		RAMTotalBytes: 32 * gib, RAMAvailableBytes: 24 * gib,
		GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 12 * gib, FreeBytes: 10 * gib}},
	}}, func() (llamacpp.Profile, error) {
		return llamacpp.Profile{Options: []llamacpp.Option{
			{Key: "ctx-size"}, {Key: "n-gpu-layers"}, {Key: "no-kv-offload"},
		}}, nil
	})

	url := "/api/v1/models/" + model.ID + "/recommendation?instance_id=" + instance.ID +
		"&context_length=4096&gpu_mode=manual&gpu_devices=CUDA0,CUDA0&tensor_split=1&system_spillover_enabled=true"
	w := doRequest(t, handler, http.MethodGet, url, nil, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("runtime preview=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"context_length":4096`) || !strings.Contains(w.Body.String(), `"CUDA0"`) {
		t.Fatalf("runtime preview body=%s", w.Body.String())
	}

	otherPath := filepath.Join(f.dir, "other-runtime.gguf")
	writeAPIMetadataGGUF(t, otherPath, "qwen2", 8192)
	other, err := f.models.Create(t.Context(), models.CreateModelInput{Name: "Other runtime", GGUFPath: otherPath})
	if err != nil {
		t.Fatal(err)
	}
	otherInstance, err := f.server.lifecycle.Instances().Create(t.Context(), instances.CreateInput{
		ModelID: other.ID, Name: "Other instance", Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	w = doRequest(t, handler, http.MethodGet,
		"/api/v1/models/"+model.ID+"/recommendation?instance_id="+otherInstance.ID, nil, cookie)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "does not belong to model") {
		t.Fatalf("model binding=%d body=%s", w.Code, w.Body.String())
	}

	profileErrorHandler := NewRecommendationHandler(f.auth, f.models, staticHardware{snapshot: hardware.Snapshot{
		RAMTotalBytes: 32 * gib, RAMAvailableBytes: 24 * gib,
	}}, func() (llamacpp.Profile, error) { return llamacpp.Profile{}, errors.New("profile unavailable") })
	w = doRequest(t, profileErrorHandler, http.MethodGet,
		"/api/v1/models/"+model.ID+"/recommendation?instance_id="+instance.ID+"&context_length=4096", nil, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("profile fallback=%d body=%s", w.Code, w.Body.String())
	}
}


func TestRecommendationUnboundSpilloverDefaultsDisabled(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	const gib int64 = 1024 * 1024 * 1024
	path := filepath.Join(f.dir, "spill-default.gguf")
	writeAPIMetadataGGUF(t, path, "qwen2", 32768)
	if err := os.Truncate(path, 8*gib); err != nil {
		t.Fatal(err)
	}
	model, err := f.models.Create(t.Context(), models.CreateModelInput{Name: "Spill default", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewRecommendationHandler(f.auth, f.models, staticHardware{snapshot: hardware.Snapshot{
		RAMTotalBytes: 32 * gib, RAMAvailableBytes: 24 * gib,
		GPUs: []hardware.GPU{{ID: "CUDA0", TotalBytes: 4 * gib, FreeBytes: 4 * gib}},
	}}, func() (llamacpp.Profile, error) {
		return llamacpp.Profile{Options: []llamacpp.Option{{Key: "n-gpu-layers"}}}, nil
	})

	w := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/recommendation?context_length=4096", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"current_fit":false`) {
		t.Fatalf("default spillover must remain disabled: %d %s", w.Code, w.Body.String())
	}
	w = doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/recommendation?context_length=4096&system_spillover_enabled=true", nil, cookie)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"current_fit":true`) {
		t.Fatalf("explicit spillover preview should be runnable: %d %s", w.Code, w.Body.String())
	}
}

func TestRecommendationExplicitEmptyManualOverridesClearPersistedValues(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	model := createModel(t, f, cookie)
	enabled := true
	instance, err := f.server.lifecycle.Instances().Create(t.Context(), instances.CreateInput{
		ModelID: model.ID, Name: "Manual clear preview", Enabled: &enabled,
		GPUMode: "manual", GPUDevices: []string{"CUDA0", "CUDA1"}, TensorSplit: "9,1",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewRecommendationHandler(f.auth, f.models, staticHardware{snapshot: hardware.Snapshot{
		RAMTotalBytes: 16 << 30, RAMAvailableBytes: 12 << 30,
		GPUs: []hardware.GPU{{ID: "CUDA0", FreeBytes: 8 << 30}, {ID: "CUDA1", FreeBytes: 8 << 30}},
	}})

	w := doRequest(t, handler, http.MethodGet,
		"/api/v1/models/"+model.ID+"/recommendation?instance_id="+instance.ID+"&gpu_mode=manual&gpu_devices=CUDA0,CUDA1&tensor_split=",
		nil, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("clear tensor split=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"tensor_split":"9,1"`) {
		t.Fatalf("persisted tensor split leaked into explicit empty preview: %s", w.Body.String())
	}

	w = doRequest(t, handler, http.MethodGet,
		"/api/v1/models/"+model.ID+"/recommendation?instance_id="+instance.ID+"&gpu_mode=manual&gpu_devices=&tensor_split=",
		nil, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("clear manual devices=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "CUDA0") || strings.Contains(w.Body.String(), "CUDA1") {
		t.Fatalf("persisted manual devices leaked into explicit empty preview: %s", w.Body.String())
	}
}


func TestRecommendationPreviewOptionsAreValidated(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	model := createModel(t, f, cookie)
	handler := NewRecommendationHandler(f.auth, f.models, staticHardware{snapshot: hardware.Snapshot{RAMAvailableBytes: 16 << 30}}, func() (llamacpp.Profile, error) {
		return llamacpp.Profile{Version: "test", Options: []llamacpp.Option{{Key: "ctx-size", Kind: "integer"}, {Key: "n-gpu-layers", Kind: "integer"}, {Key: "mmproj", Kind: "string"}}}, nil
	})
	w := doRequest(t, handler, http.MethodGet,
		"/api/v1/models/"+model.ID+"/recommendation?preview_options=%7B%22made-up%22%3A%221%22%7D", nil, cookie)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unsupported") {
		t.Fatalf("unsupported preview=%d body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, handler, http.MethodGet,
		"/api/v1/models/"+model.ID+"/recommendation?preview_options=%7B%22mmproj%22%3A%22%22%2C%22n-gpu-layers%22%3A%222%22%7D", nil, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("validated preview=%d body=%s", w.Code, w.Body.String())
	}
}
