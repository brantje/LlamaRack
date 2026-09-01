package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsHandlerInstanceStateGauges(t *testing.T) {
	s := testService(t)
	h := NewMetricsHandler(s, nil, func() map[string]string {
		return map[string]string{"ready\"one": "ready", "stopped": "UNLOADED", "unknown": "mystery"}
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, expected := range []string{
		`llamarack_instance_state{instance_id="ready\"one",state="READY"} 1`,
		`llamarack_instance_state{instance_id="ready\"one",state="FAILED"} 0`,
		`llamarack_instance_state{instance_id="stopped",state="UNLOADED"} 1`,
		`llamarack_instance_state{instance_id="stopped",state="DRAINING"} 0`,
		`llamarack_instance_state{instance_id="unknown",state="READY"} 0`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %q in metrics:\n%s", expected, body)
		}
	}
	if strings.Contains(body, "llamacpp_manager_") {
		t.Fatalf("previous product metrics still emitted:\n%s", body)
	}
}
