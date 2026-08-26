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

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		name       string
		origin     string
		host       string
		configured string
		want       bool
	}{
		{"explicit", "https://ui.example.test", "api.example.test:8888", "http://localhost:3000, https://ui.example.test", true},
		{"same LAN host", "http://192.168.60.5:3000", "192.168.60.5:8888", "http://localhost:3000", true},
		{"same DNS host case insensitive", "https://MANAGER.local:3000", "manager.local:8888", "", true},
		{"different host", "http://192.168.60.6:3000", "192.168.60.5:8888", "http://localhost:3000", false},
		{"bad origin", "://bad", "localhost:8888", "", false},
		{"unsupported scheme", "ftp://localhost:3000", "localhost:8888", "", false},
		{"empty hostname", "http:///path", "localhost:8888", "", false},
		{"bad request host", "http://localhost:3000", "[bad", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := originAllowed(tc.origin, tc.host, tc.configured); got != tc.want {
				t.Fatalf("originAllowed(%q,%q,%q)=%v want %v", tc.origin, tc.host, tc.configured, got, tc.want)
			}
		})
	}
}

func TestCORSMiddleware(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusCreated)
	})
	h := cors("http://localhost:3000", next)

	r := httptest.NewRequest(http.MethodGet, "http://192.168.60.5:8888/health", nil)
	r.Host = "192.168.60.5:8888"
	r.Header.Set("Origin", "http://192.168.60.5:3000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !nextCalled || w.Code != http.StatusCreated {
		t.Fatalf("request status=%d called=%v", w.Code, nextCalled)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://192.168.60.5:3000" {
		t.Fatalf("allow origin=%q", got)
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "true" || w.Header().Get("Vary") != "Origin" {
		t.Fatalf("cors headers=%v", w.Header())
	}

	nextCalled = false
	r = httptest.NewRequest(http.MethodOptions, "http://192.168.60.5:8888/api/v1/me", nil)
	r.Host = "192.168.60.5:8888"
	r.Header.Set("Origin", "http://192.168.60.5:3000")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || nextCalled {
		t.Fatalf("preflight status=%d called=%v", w.Code, nextCalled)
	}

	r = httptest.NewRequest(http.MethodGet, "http://192.168.60.5:8888/health", nil)
	r.Host = "192.168.60.5:8888"
	r.Header.Set("Origin", "http://evil.example:3000")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected origin header: %v", w.Header())
	}
}
