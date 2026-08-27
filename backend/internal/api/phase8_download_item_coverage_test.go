package api

import (
	"net/http"
	"testing"
)

func TestPhase8DownloadItemNotFoundBranches(t *testing.T) {
	fixture := newPhase8Fixture(t)
	tests := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"missing get", http.MethodGet, "/api/v1/downloads/missing", http.StatusNotFound},
		{"missing delete", http.MethodDelete, "/api/v1/downloads/missing", http.StatusNotFound},
		{"missing cancel", http.MethodPost, "/api/v1/downloads/missing/cancel", http.StatusNotFound},
		{"missing retry", http.MethodPost, "/api/v1/downloads/missing/retry", http.StatusBadRequest},
		{"action requires post", http.MethodGet, "/api/v1/downloads/missing/cancel", http.StatusNotFound},
		{"empty item", http.MethodGet, "/api/v1/downloads//", http.StatusNotFound},
		{"too many segments", http.MethodPost, "/api/v1/downloads/a/b/c", http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := phase8Request(t, fixture, tc.method, tc.path, nil, true)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
