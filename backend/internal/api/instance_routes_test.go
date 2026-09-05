package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/lifecycle"
	"github.com/brantje/llamarack/backend/internal/supervisor"
)

func TestInstanceRoutesCoverCRUDLifecycleAndRunningReconfigure(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)
	model := createModel(t, f, cookie)

	sup := supervisor.New(fakeAPILogServer(t), "127.0.0.1", 33600, 5*time.Second)
	f.server.lifecycle = lifecycle.New(f.models, sup)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		sup.Shutdown(ctx)
	})

	create := doRequest(t, f.server, http.MethodPost, "/api/v1/instances", map[string]any{
		"model_id":             model.ID,
		"name":                 "Primary Coder",
		"enabled":              true,
		"autoload_enabled":     true,
		"always_on":            false,
		"priority":             "high",
		"eviction_enabled":     true,
		"idle_unload_seconds":  120,
		"max_pending_requests": 8,
		"gpu_mode":             "manual",
		"gpu_devices":          []string{"0", "1"},
		"tensor_split":         "1,1",
		"options":              map[string]string{"ctx-size": "8192", "threads": "4"},
	}, cookie)
	if create.Code != http.StatusCreated {
		t.Fatalf("create instance=%d body=%s", create.Code, create.Body.String())
	}
	var created instances.Instance
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.ID == created.Slug || created.Slug != "primary-coder" || created.ModelID != model.ID || created.MaxPendingRequests != 8 {
		t.Fatalf("created=%+v", created)
	}

	for _, tc := range []struct {
		path string
		want int
		text string
	}{
		{"/api/v1/instances", http.StatusOK, "primary-coder"},
		{"/api/v1/instances/primary-coder", http.StatusOK, "Primary Coder"},
		{"/api/v1/instances/primary-coder/options", http.StatusOK, "ctx-size"},
		{"/api/v1/instances/primary-coder/runtime", http.StatusOK, "UNLOADED"},
		{"/api/v1/instances/primary-coder/logs", http.StatusOK, "lines"},
	} {
		w := doRequest(t, f.server, http.MethodGet, tc.path, nil, cookie)
		if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.text) {
			t.Fatalf("GET %s=%d body=%s", tc.path, w.Code, w.Body.String())
		}
	}

	baseUpdate := map[string]any{
		"model_id":            model.ID,
		"name":                "Primary Coder",
		"enabled":             true,
		"autoload_enabled":    true,
		"always_on":           true,
		"priority":            "normal",
		"eviction_enabled":    false,
		"idle_unload_seconds": 45,
		"gpu_mode":            "auto",
		"gpu_devices":         []string{},
		"tensor_split":        "",
		"options":             map[string]string{"ctx-size": "4096"},
	}
	slugAwareUpdate := map[string]any{}
	for k, v := range baseUpdate {
		slugAwareUpdate[k] = v
	}
	slugAwareUpdate["slug"] = "Primary Coder"
	updated := doRequest(t, f.server, http.MethodPut, "/api/v1/instances/primary-coder", slugAwareUpdate, cookie)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"always_on":true`) {
		t.Fatalf("update unloaded=%d body=%s", updated.Code, updated.Body.String())
	}

	nameOnlyUpdate := map[string]any{}
	for k, v := range baseUpdate {
		nameOnlyUpdate[k] = v
	}
	nameOnlyUpdate["name"] = "Renamed Coder"
	w := doRequest(t, f.server, http.MethodPut, "/api/v1/instances/primary-coder", nameOnlyUpdate, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("name-only update=%d body=%s", w.Code, w.Body.String())
	}
	var nameOnly instances.Instance
	if err := json.Unmarshal(w.Body.Bytes(), &nameOnly); err != nil {
		t.Fatal(err)
	}
	if nameOnly.ID != created.ID || nameOnly.Slug != "primary-coder" || nameOnly.Name != "Renamed Coder" {
		t.Fatalf("name-only update changed identity: %+v", nameOnly)
	}

	duplicate := doRequest(t, f.server, http.MethodPost, "/api/v1/instances/primary-coder/duplicate", nil, cookie)
	if duplicate.Code != http.StatusCreated || !strings.Contains(duplicate.Body.String(), "primary-coder-copy") {
		t.Fatalf("duplicate=%d body=%s", duplicate.Code, duplicate.Body.String())
	}

	started := doRequest(t, f.server, http.MethodPost, "/api/v1/instances/primary-coder/start", nil, cookie)
	if started.Code != http.StatusAccepted || !strings.Contains(started.Body.String(), "READY") {
		t.Fatalf("start=%d body=%s", started.Code, started.Body.String())
	}

	runningNoRestart := doRequest(t, f.server, http.MethodPut, "/api/v1/instances/primary-coder", baseUpdate, cookie)
	if runningNoRestart.Code != http.StatusConflict || !strings.Contains(runningNoRestart.Body.String(), "must restart") {
		t.Fatalf("running update without restart=%d body=%s", runningNoRestart.Code, runningNoRestart.Body.String())
	}

	runningRename := map[string]any{}
	for k, v := range baseUpdate {
		runningRename[k] = v
	}
	runningRename["name"] = "Renamed Coder"
	runningRename["slug"] = "Renamed Coder"
	runningRename["restart_running"] = true
	runningRename["confirm_slug_change"] = true
	runningRename["always_on"] = false
	w = doRequest(t, f.server, http.MethodPut, "/api/v1/instances/primary-coder", runningRename, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("running rename=%d body=%s", w.Code, w.Body.String())
	}
	var renamed instances.Instance
	if err := json.Unmarshal(w.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.ID != created.ID || renamed.Slug != "renamed-coder" {
		t.Fatalf("slug rename changed durable identity: created=%+v renamed=%+v", created, renamed)
	}

	restarted := doRequest(t, f.server, http.MethodPost, "/api/v1/instances/renamed-coder/restart", nil, cookie)
	if restarted.Code != http.StatusAccepted {
		t.Fatalf("restart=%d body=%s", restarted.Code, restarted.Body.String())
	}
	if got := doRequest(t, f.server, http.MethodGet, "/api/v1/instances/renamed-coder/runtime", nil, cookie); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "READY") {
		t.Fatalf("runtime after restart=%d body=%s", got.Code, got.Body.String())
	}

	killed := doRequest(t, f.server, http.MethodPost, "/api/v1/instances/renamed-coder/kill", nil, cookie)
	if killed.Code != http.StatusNoContent {
		t.Fatalf("kill=%d body=%s", killed.Code, killed.Body.String())
	}
	stopped := doRequest(t, f.server, http.MethodPost, "/api/v1/instances/renamed-coder/stop", nil, cookie)
	if stopped.Code != http.StatusNoContent {
		t.Fatalf("stop=%d body=%s", stopped.Code, stopped.Body.String())
	}

	for _, path := range []string{
		"/api/v1/instances/renamed-coder/start",
		"/api/v1/instances/renamed-coder/stop",
		"/api/v1/instances/renamed-coder/restart",
		"/api/v1/instances/renamed-coder/kill",
		"/api/v1/instances/renamed-coder/duplicate",
		"/api/v1/instances/renamed-coder/runtime",
		"/api/v1/instances/renamed-coder/options",
		"/api/v1/instances/renamed-coder/logs",
		"/api/v1/instances/renamed-coder/logs/stream",
	} {
		badMethod := doRequest(t, f.server, http.MethodPatch, path, map[string]any{}, cookie)
		if badMethod.Code != http.StatusMethodNotAllowed {
			t.Fatalf("PATCH %s=%d body=%s", path, badMethod.Code, badMethod.Body.String())
		}
	}

	if removed := doRequest(t, f.server, http.MethodDelete, "/api/v1/instances/primary-coder-copy", nil, cookie); removed.Code != http.StatusNoContent {
		t.Fatalf("delete duplicate=%d body=%s", removed.Code, removed.Body.String())
	}
	if removed := doRequest(t, f.server, http.MethodDelete, "/api/v1/instances/renamed-coder", nil, cookie); removed.Code != http.StatusNoContent {
		t.Fatalf("delete renamed=%d body=%s", removed.Code, removed.Body.String())
	}
	if missing := doRequest(t, f.server, http.MethodDelete, "/api/v1/instances/renamed-coder", nil, cookie); missing.Code != http.StatusNotFound {
		t.Fatalf("delete missing=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestModelCreationFirstInstanceBranches(t *testing.T) {
	f := newAPIFixture(t, nil)
	cookie := bootstrapAndLogin(t, f)

	writeGGUF := func(name string) string {
		path := filepath.Join(f.dir, name)
		if err := os.WriteFile(path, []byte("gguf"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	started := doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]any{
		"name":      "Bootstrap Model",
		"gguf_path": writeGGUF("bootstrap-Q4_K_M.gguf"),
		"first_instance": map[string]any{
			"name":             "Bootstrap Instance",
			"slug":             "Bootstrap API",
			"always_on":        false,
			"autoload_enabled": true,
			"eviction_enabled": true,
			"start":            true,
		},
	}, cookie)
	if started.Code != http.StatusCreated || !strings.Contains(started.Body.String(), `"slug":"bootstrap-api"`) || !strings.Contains(started.Body.String(), `"start_error"`) {
		t.Fatalf("bootstrap start failure=%d body=%s", started.Code, started.Body.String())
	}

	invalidInstance := doRequest(t, f.server, http.MethodPost, "/api/v1/models", map[string]any{
		"name":           "Invalid Bootstrap",
		"gguf_path":      writeGGUF("invalid-Q5_K_M.gguf"),
		"first_instance": map[string]any{"name": "!!!"},
	}, cookie)
	if invalidInstance.Code != http.StatusBadRequest || !strings.Contains(invalidInstance.Body.String(), `"model"`) {
		t.Fatalf("invalid first instance=%d body=%s", invalidInstance.Code, invalidInstance.Body.String())
	}
}
