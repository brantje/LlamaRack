package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/models"
)

func TestModelMetadataValueHandlerBranches(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	handler := NewModelMetadataValueHandler(f.auth, f.models)

	if response := doRequest(t, handler, http.MethodPost, "/api/v1/models/missing/details/value?key=x", nil, cookie); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method=%d", response.Code)
	}
	if response := doRequest(t, handler, http.MethodGet, "/api/v1/models/missing/details/value?key=x", nil, cookie); response.Code != http.StatusNotFound {
		t.Fatalf("missing model=%d", response.Code)
	}

	badPath := filepath.Join(f.dir, "bad-value.gguf")
	if err := os.WriteFile(badPath, []byte("not gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	badModel, err := f.models.Create(t.Context(), models.CreateModelInput{Name: "Bad Value", GGUFPath: badPath})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/v1/models/" + badModel.ID + "/details/value?key=x"
	if response := doRequest(t, handler, http.MethodGet, endpoint, nil, cookie); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed=%d body=%s", response.Code, response.Body.String())
	}

	goodPath := filepath.Join(f.dir, "good-value.gguf")
	writeAPIMetadataGGUF(t, goodPath, "qwen2", 32768)
	goodModel, err := f.models.Create(t.Context(), models.CreateModelInput{Name: "Good Value", GGUFPath: goodPath})
	if err != nil {
		t.Fatal(err)
	}
	goodEndpoint := "/api/v1/models/" + goodModel.ID + "/details/value?key=vendor.future.key"
	if response := doRequest(t, handler, http.MethodGet, goodEndpoint+"&limit=bad", nil, cookie); response.Code != http.StatusBadRequest {
		t.Fatalf("bad limit=%d", response.Code)
	}
	if response := doRequest(t, handler, http.MethodGet, goodEndpoint+"&offset=1&limit=2", nil, cookie); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"offset":1`) {
		t.Fatalf("paged=%d body=%s", response.Code, response.Body.String())
	}
	if offset, limit, ok := metadataValuePage(httptestRequest("?offset=3&limit=4")); !ok || offset != 3 || limit != 4 {
		t.Fatalf("page=%d %d %v", offset, limit, ok)
	}
}

func TestModelCreateWrapperLeavesNonModelResponsesUntouched(t *testing.T) {
	f := newAPIFixture(t, nil)
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "plain", body: "plain response"},
		{name: "json without model", body: `{"ok":true}`},
		{name: "invalid model", body: `{"model":"bad"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) })
			handler := NewModelCreateHandler(next, f.models)
			response := doRequest(t, handler, http.MethodPost, "/api/v1/models", nil, nil)
			if response.Code != http.StatusOK || response.Body.String() != test.body {
				t.Fatalf("response=%d %q", response.Code, response.Body.String())
			}
		})
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]any{"model": models.Model{ID: "missing"}})
	})
	handler := NewModelCreateHandler(next, f.models)
	response := doRequest(t, handler, http.MethodPost, "/api/v1/models", nil, nil)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"id":"missing"`) {
		t.Fatalf("refresh failure changed response=%d %s", response.Code, response.Body.String())
	}
}
