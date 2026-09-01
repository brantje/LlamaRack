package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/llamacpp"
)

func TestAPIValidatesLlamaCppOptionsBeforeSaving(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	writeModel := func(name string) string {
		path := filepath.Join(f.dir, name)
		if err := os.WriteFile(path, []byte("gguf"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	for _, tc := range []struct {
		name string
		opts map[string]string
		text string
	}{
		{"unknown", map[string]string{"not-real": "1"}, "unsupported llama.cpp option"},
		{"bad-type", map[string]string{"ctx-size": "huge"}, "integer value"},
		{"reserved", map[string]string{"port": "9999"}, "managed by LlamaRack"},
		{"flag-needs-bool", map[string]string{"flash-attn": "yes"}, "true or false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]any{
				"name": tc.name, "gguf_path": writeModel(tc.name + ".gguf"), "options": tc.opts,
			}, cookie)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.text) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}

	valid := doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "Valid", "gguf_path": writeModel("valid.gguf"),
		"options": map[string]string{"--ctx-size": "2048", "flash-attn": "true", "cache-type-k": "q8_0"},
	}, cookie)
	if valid.Code != http.StatusCreated {
		t.Fatalf("valid model=%d body=%s", valid.Code, valid.Body.String())
	}
	var modelResponse struct {
		Model struct {
			ID string `json:"id"`
		} `json:"model"`
	}
	if err := decodeJSONBody(valid.Body.Bytes(), &modelResponse); err != nil {
		t.Fatal(err)
	}
	if modelResponse.Model.ID == "" {
		t.Fatalf("missing model id: %s", valid.Body.String())
	}
	options := doRequest(t, f.server, http.MethodGet, "/api/v1/models/"+modelResponse.Model.ID+"/options", nil, cookie)
	if options.Code != http.StatusOK || !strings.Contains(options.Body.String(), `"ctx-size":"2048"`) || strings.Contains(options.Body.String(), `"--ctx-size"`) {
		t.Fatalf("canonical options=%d body=%s", options.Code, options.Body.String())
	}

	instance := doRequest(t, f.server, http.MethodPost, "/api/v1/instances", map[string]any{
		"model_id": modelResponse.Model.ID, "name": "Validated Instance",
		"options": map[string]string{"threads": "many"},
	}, cookie)
	if instance.Code != http.StatusBadRequest || !strings.Contains(instance.Body.String(), "integer value") {
		t.Fatalf("invalid instance options=%d body=%s", instance.Code, instance.Body.String())
	}

	update := doRequest(t, f.server, http.MethodPut, "/api/v1/models/"+modelResponse.Model.ID, map[string]any{
		"name": "Valid", "context_length": 0, "options": map[string]string{"chat-template": ""},
	}, cookie)
	if update.Code != http.StatusBadRequest || !strings.Contains(update.Body.String(), "requires STRING") {
		t.Fatalf("invalid model update=%d body=%s", update.Code, update.Body.String())
	}
}

func TestAPIRejectsOverridesWhenLlamaSchemaUnavailable(t *testing.T) {
	f := newAPIFixture(t, func() (llamacpp.Profile, error) { return llamacpp.Profile{}, errors.New("binary unavailable") })
	cookie := bootstrapAndLogin(t, f)
	path := filepath.Join(f.dir, "unavailable.gguf")
	if err := os.WriteFile(path, []byte("gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "Unavailable", "gguf_path": path, "options": map[string]string{"ctx-size": "1024"},
	}, cookie)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "cannot validate llama.cpp options") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	clearOnly := doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]any{
		"name": "No Options", "gguf_path": path, "options": map[string]string{},
	}, cookie)
	if clearOnly.Code != http.StatusCreated {
		t.Fatalf("empty options should not require schema: %d body=%s", clearOnly.Code, clearOnly.Body.String())
	}
}

func decodeJSONBody(data []byte, target any) error {
	return json.Unmarshal(data, target)
}
