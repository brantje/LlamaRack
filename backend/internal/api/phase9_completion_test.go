package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/models"
)

func TestPhase9ModelCreateResponseRefreshesLogicalSplitSize(t *testing.T) {
	f := newAPIFixture(t, nil)
	first := filepath.Join(f.dir, "split-Q4_K_M-00001-of-00002.gguf")
	second := filepath.Join(f.dir, "split-Q4_K_M-00002-of-00002.gguf")
	if err := os.WriteFile(first, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("defgh"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := f.models.Create(t.Context(), models.CreateModelInput{Name: "Split", GGUFPath: first})
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]any{"model": model})
	})
	handler := NewPhase9ModelCreateHandler(next, f.models)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/models", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d", response.Code)
	}
	var payload struct {
		Model models.Model `json:"model"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Model.TotalBytes != 8 {
		t.Fatalf("response logical bytes=%d body=%s", payload.Model.TotalBytes, response.Body.String())
	}
	stored, err := f.models.GetByID(t.Context(), model.ID)
	if err != nil || stored.TotalBytes != 8 {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestPhase9DetailsUsesPrimarySplitMetadataAndLogicalSize(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	first := filepath.Join(f.dir, "metadata-Q4_K_M-00001-of-00002.gguf")
	second := filepath.Join(f.dir, "metadata-Q4_K_M-00002-of-00002.gguf")
	writeAPIMetadataGGUF(t, first, "qwen2", 32768)
	if err := os.WriteFile(second, []byte("secondary-shard"), 0o644); err != nil {
		t.Fatal(err)
	}
	firstInfo, _ := os.Stat(first)
	secondInfo, _ := os.Stat(second)
	model, err := f.models.Create(t.Context(), models.CreateModelInput{Name: "Split Metadata", GGUFPath: first})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewPhase9ModelDetailsHandler(f.auth, f.models)
	response := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/details", nil, cookie)
	wantTotal := firstInfo.Size() + secondInfo.Size()
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"key":"qwen2.context_length"`) || !strings.Contains(response.Body.String(), `"total_bytes":`+jsonNumber(wantTotal)) {
		t.Fatalf("details=%d body=%s want total=%d", response.Code, response.Body.String(), wantTotal)
	}
}

func TestPhase9MetadataValueHandler(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	path := filepath.Join(f.dir, "metadata.gguf")
	writeAPIMetadataGGUF(t, path, "qwen2", 32768)
	model, err := f.models.Create(t.Context(), models.CreateModelInput{Name: "Metadata", GGUFPath: path})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewPhase9ModelMetadataValueHandler(f.auth, f.models)
	endpoint := "/api/v1/models/" + model.ID + "/details/value?key=vendor.future.key"
	if response := doRequest(t, handler, http.MethodGet, endpoint, nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", response.Code)
	}
	response := doRequest(t, handler, http.MethodGet, endpoint, nil, cookie)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"value":"visible"`) || !strings.Contains(response.Body.String(), `"type":"string"`) {
		t.Fatalf("value=%d body=%s", response.Code, response.Body.String())
	}
	if response := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/details/value", nil, cookie); response.Code != http.StatusBadRequest {
		t.Fatalf("missing key=%d", response.Code)
	}
	if response := doRequest(t, handler, http.MethodGet, endpoint+"&offset=bad", nil, cookie); response.Code != http.StatusBadRequest {
		t.Fatalf("bad page=%d", response.Code)
	}
	if response := doRequest(t, handler, http.MethodGet, "/api/v1/models/"+model.ID+"/details/value?key=missing", nil, cookie); response.Code != http.StatusNotFound {
		t.Fatalf("missing metadata=%d", response.Code)
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
