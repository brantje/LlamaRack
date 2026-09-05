package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstanceAndModelRouteErrors(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	model := createModel(t, f, cookie)

	create := doRequest(t, f.server, http.MethodPost, "/api/v1/instances", map[string]any{
		"model_id": model.ID, "name": "Disabled Instance", "enabled": false,
	}, cookie)
	if create.Code != http.StatusCreated {
		t.Fatalf("create disabled=%d body=%s", create.Code, create.Body.String())
	}

	for _, tc := range []struct {
		method string
		path   string
		body   any
		want   int
	}{
		{http.MethodPost, "/api/v1/instances", []byte(`{"model_id":`), http.StatusBadRequest},
		{http.MethodGet, "/api/v1/instances/", nil, http.StatusOK},
		{http.MethodPatch, "/api/v1/instances/disabled-instance", map[string]any{}, http.StatusMethodNotAllowed},
		{http.MethodPut, "/api/v1/instances/disabled-instance", []byte(`{"name":`), http.StatusBadRequest},
		{http.MethodPut, "/api/v1/instances/missing", map[string]any{"model_id": model.ID, "name": "Missing"}, http.StatusNotFound},
		{http.MethodPut, "/api/v1/instances/disabled-instance", map[string]any{"model_id": model.ID, "name": "Display name may be punctuation", "slug": "!!!", "confirm_slug_change": true}, http.StatusBadRequest},
		{http.MethodPost, "/api/v1/instances/disabled-instance/start", nil, http.StatusServiceUnavailable},
		{http.MethodPost, "/api/v1/instances/missing/restart", nil, http.StatusNotFound},
		{http.MethodPost, "/api/v1/instances/missing/kill", nil, http.StatusNotFound},
		{http.MethodPost, "/api/v1/instances/missing/duplicate", nil, http.StatusNotFound},
		{http.MethodGet, "/api/v1/instances/missing/options", nil, http.StatusNotFound},
		{http.MethodGet, "/api/v1/instances/disabled-instance/extra/path", nil, http.StatusNotFound},
		{http.MethodPatch, "/api/v1/models/" + model.ID, map[string]any{}, http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v1/models/" + model.ID + "/options", nil, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/models/" + model.ID + "/start", nil, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/models/" + model.ID + "/stop", nil, http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v1/models/" + model.ID + "/runtime", nil, http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/v1/models/" + model.ID + "/too/many", nil, http.StatusNotFound},
	} {
		w := doRequest(t, f.server, tc.method, tc.path, tc.body, cookie)
		if w.Code != tc.want {
			t.Fatalf("%s %s=%d want=%d body=%s", tc.method, tc.path, w.Code, tc.want, w.Body.String())
		}
	}

	update := doRequest(t, f.server, http.MethodPut, "/api/v1/models/"+model.ID, map[string]any{
		"name": "Updated API Model", "context_length": 16384,
		"options": map[string]string{"ctx-size": "16384", "threads": "6"},
	}, cookie)
	if update.Code != http.StatusOK || !strings.Contains(update.Body.String(), "Updated API Model") {
		t.Fatalf("model update=%d body=%s", update.Code, update.Body.String())
	}
	options := doRequest(t, f.server, http.MethodGet, "/api/v1/models/"+model.ID+"/options", nil, cookie)
	if options.Code != http.StatusOK || !strings.Contains(options.Body.String(), "16384") {
		t.Fatalf("model options=%d body=%s", options.Code, options.Body.String())
	}
}

func TestModelCreateValidationBranches(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)

	badJSON := doRequest(t, f.server, http.MethodPost, "/api/v1/models", []byte(`{"name":`), cookie)
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON=%d body=%s", badJSON.Code, badJSON.Body.String())
	}
	missing := doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]any{"name": "Missing file", "gguf_path": "nope.gguf"}, cookie)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing model file=%d body=%s", missing.Code, missing.Body.String())
	}

	path := filepath.Join(f.dir, "registry-only-Q8_0.gguf")
	if err := os.WriteFile(path, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	registryOnly := doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "Registry Only", "gguf_path": path,
	}, cookie)
	if registryOnly.Code != http.StatusCreated || strings.Contains(registryOnly.Body.String(), `"instance"`) {
		t.Fatalf("registry-only=%d body=%s", registryOnly.Code, registryOnly.Body.String())
	}
}
