package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagementStatusRecorderAndLifecycleActions(t *testing.T) {
	base := httptest.NewRecorder()
	recorder := &managementStatusRecorder{ResponseWriter: base}
	if got := recorder.StatusCode(); got != http.StatusOK {
		t.Fatalf("default status=%d", got)
	}
	if _, err := recorder.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if recorder.StatusCode() != http.StatusOK || base.Body.String() != "ok" {
		t.Fatalf("recorder status=%d body=%q", recorder.StatusCode(), base.Body.String())
	}

	base = httptest.NewRecorder()
	recorder = &managementStatusRecorder{ResponseWriter: base}
	recorder.WriteHeader(http.StatusAccepted)
	recorder.WriteHeader(http.StatusInternalServerError)
	if recorder.StatusCode() != http.StatusAccepted || base.Code != http.StatusAccepted {
		t.Fatalf("first status should win: recorder=%d base=%d", recorder.StatusCode(), base.Code)
	}

	for path, want := range map[string]string{
		"/api/v1/instances/alpha/start":   "started",
		"/api/v1/instances/alpha/stop":    "stopped",
		"/api/v1/instances/alpha/restart": "restarted",
		"/api/v1/instances/alpha/kill":    "killed",
	} {
		id, action, ok := lifecycleAction(path, http.MethodPost)
		if !ok || id != "alpha" || action != want {
			t.Fatalf("lifecycleAction(%q)=(%q,%q,%v)", path, id, action, ok)
		}
	}

	for _, test := range []struct{ path, method string }{
		{"/api/v1/instances/alpha/start", http.MethodGet},
		{"/api/v1/models/alpha/start", http.MethodPost},
		{"/api/v1/instances//start", http.MethodPost},
		{"/api/v1/instances/alpha", http.MethodPost},
		{"/api/v1/instances/alpha/duplicate", http.MethodPost},
		{"/api/v1/instances/alpha/start/extra", http.MethodPost},
	} {
		if _, _, ok := lifecycleAction(test.path, test.method); ok {
			t.Fatalf("unexpected lifecycle action for %s %s", test.method, test.path)
		}
	}
}
