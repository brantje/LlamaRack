package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsExposeManagerGoroutineGauge(t *testing.T) {
	handler := NewMetricsHandler(testService(t), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "# TYPE llamarack_manager_goroutines gauge") || !strings.Contains(body, "llamarack_manager_goroutines ") {
		t.Fatalf("goroutine gauge missing from metrics:\n%s", body)
	}
}
