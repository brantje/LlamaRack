package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/database"
	managersecurity "github.com/brantje/llamacpp-manager/backend/internal/security"
	"github.com/brantje/llamacpp-manager/backend/internal/settings"
)

func TestMuxRoutingDoesNotConflict(t *testing.T) {
	apiHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	openAIHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	mux := newMux(apiHandler, openAIHandler)
	cases := []struct { method, path string; want int }{
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
			if w.Code != tc.want { t.Fatalf("%s %s status=%d want=%d", tc.method, tc.path, w.Code, tc.want) }
		})
	}
}

func testCORSNetwork(t *testing.T) (*managersecurity.Network, *settings.Service) {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "cors.db"))
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { _ = db.Close() })
	store := settings.New(db, settings.Defaults{SessionLifetime: time.Hour, AllowedOrigins: "https://manager.example.com", StartupTimeout: time.Minute, AlwaysOnReconcile: time.Second})
	return managersecurity.NewNetwork(store), store
}

func TestDynamicCORSAllowsConfiguredAndSameOriginRequests(t *testing.T) {
	network, _ := testCORSNetwork(t)
	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called++; w.WriteHeader(http.StatusAccepted) })
	h := dynamicCORS(network, next)
	for _, origin := range []string{"https://manager.example.com", "http://manager.local"} {
		r := httptest.NewRequest(http.MethodPost, "http://manager.local/api/v1/test", nil)
		r.Host = "manager.local"
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusAccepted { t.Fatalf("origin %q status=%d", origin, w.Code) }
		if w.Header().Get("Access-Control-Allow-Origin") != origin { t.Fatalf("origin %q allow-origin=%q", origin, w.Header().Get("Access-Control-Allow-Origin")) }
		if w.Header().Get("Access-Control-Allow-Credentials") != "" || w.Header().Get("Vary") != "Origin" { t.Fatalf("origin %q unexpected CORS response headers: %v", origin, w.Header()) }
		if w.Header().Get("Access-Control-Allow-Headers") == "" || w.Header().Get("Access-Control-Allow-Methods") == "" { t.Fatalf("origin %q missing CORS capability headers", origin) }
		if !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Authorization") || strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "X-CSRF-Token") { t.Fatalf("origin %q unexpected allowed headers: %q", origin, w.Header().Get("Access-Control-Allow-Headers")) }
		if !strings.Contains(w.Header().Get("Access-Control-Expose-Headers"), "X-LiteLLM-Trace-ID") { t.Fatalf("origin %q trace header is not exposed: %q", origin, w.Header().Get("Access-Control-Expose-Headers")) }
	}
	if called != 2 { t.Fatalf("next calls=%d", called) }
}

func TestDynamicCORSRejectsForeignOriginAndHandlesPreflight(t *testing.T) {
	network, store := testCORSNetwork(t)
	called := 0
	h := dynamicCORS(network, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called++; w.WriteHeader(http.StatusAccepted) }))
	foreign := httptest.NewRequest(http.MethodGet, "http://manager.local/api", nil)
	foreign.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, foreign)
	if w.Code != http.StatusAccepted || w.Header().Get("Access-Control-Allow-Origin") != "" { t.Fatalf("foreign origin status=%d headers=%v", w.Code, w.Header()) }
	preflight := httptest.NewRequest(http.MethodOptions, "http://manager.local/api", nil)
	preflight.Header.Set("Origin", "https://manager.example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, preflight)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://manager.example.com" { t.Fatalf("preflight status=%d headers=%v", w.Code, w.Header()) }
	if called != 1 { t.Fatalf("OPTIONS should not reach next; calls=%d", called) }
	if _, err := store.Set(context.Background(), settings.AllowedOrigins, ""); err != nil { t.Fatal(err) }
	withoutOrigin := httptest.NewRequest(http.MethodGet, "http://manager.local/api", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, withoutOrigin)
	if w.Code != http.StatusAccepted || w.Header().Get("Access-Control-Allow-Origin") != "" { t.Fatalf("origin-less request status=%d headers=%v", w.Code, w.Header()) }
}
