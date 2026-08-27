package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMuxRoutingDoesNotConflict(t *testing.T) {
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	openAIHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	mux := newMux(apiHandler, openAIHandler)
	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/", http.StatusOK},
		{http.MethodPost, "/", http.StatusMethodNotAllowed},
		{http.MethodGet, "/health", http.StatusOK},
		{http.MethodGet, "/api/v1/health", http.StatusCreated},
		{http.MethodPost, "/v1/chat/completions", http.StatusAccepted},
		{http.MethodGet, "/missing", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://manager.test"+tc.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%s %s status=%d want=%d", tc.method, tc.path, w.Code, tc.want)
			}
		})
	}
}
