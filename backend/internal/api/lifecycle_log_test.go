package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLifecycleActionRoutes(t *testing.T) {
	cases := map[string]string{
		"/api/v1/instances/qwen/start":   "started",
		"/api/v1/instances/qwen/stop":    "stopped",
		"/api/v1/instances/qwen/restart": "restarted",
		"/api/v1/instances/qwen/kill":    "killed",
	}
	for path, want := range cases {
		id, action, ok := lifecycleAction(path, http.MethodPost)
		if !ok || id != "qwen" || action != want {
			t.Fatalf("%s => id=%q action=%q ok=%v", path, id, action, ok)
		}
	}
	for _, input := range []struct{ path, method string }{
		{"/api/v1/instances/qwen/start", http.MethodGet},
		{"/api/v1/instances//start", http.MethodPost},
		{"/api/v1/instances/qwen/delete", http.MethodPost},
		{"/api/v1/models/qwen/start", http.MethodPost},
	} {
		if _, _, ok := lifecycleAction(input.path, input.method); ok {
			t.Fatalf("unexpected lifecycle action for %s %s", input.method, input.path)
		}
	}
}

func TestManagementStatusRecorderCapturesImplicitAndExplicitStatus(t *testing.T) {
	base := httptest.NewRecorder()
	writer := &managementStatusRecorder{ResponseWriter: base}
	if writer.StatusCode() != http.StatusOK {
		t.Fatalf("implicit status=%d", writer.StatusCode())
	}
	writer.WriteHeader(http.StatusAccepted)
	if writer.StatusCode() != http.StatusAccepted {
		t.Fatalf("explicit status=%d", writer.StatusCode())
	}

	base = httptest.NewRecorder()
	writer = &managementStatusRecorder{ResponseWriter: base}
	if _, err := writer.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if writer.StatusCode() != http.StatusOK || base.Body.String() != "ok" {
		t.Fatalf("write status=%d body=%q", writer.StatusCode(), base.Body.String())
	}
}
