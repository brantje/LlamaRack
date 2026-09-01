package api

import (
	"net/http"
	"testing"
)

func TestHuggingFaceProviderFailuresReturnBadGateway(t *testing.T) {
	fixture := newHuggingFaceFixture(t)
	fixture.server.Close()
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/v1/huggingface/search?q=demo", nil},
		{http.MethodGet, "/api/v1/huggingface/model?repo=acme%2Fdemo", nil},
		{http.MethodPost, "/api/v1/downloads", map[string]string{"repo_id": "acme/demo", "artifact_id": "x"}},
	} {
		if got := huggingFaceRequest(t, fixture, tc.method, tc.path, tc.body, true).Code; got != http.StatusBadGateway {
			t.Fatalf("%s %s status = %d", tc.method, tc.path, got)
		}
	}
}

func TestHuggingFaceMalformedBodiesAndActionMethods(t *testing.T) {
	fixture := newHuggingFaceFixture(t)
	for _, path := range []string{"/api/v1/huggingface/token", "/api/v1/downloads"} {
		req := huggingFaceRequest(t, fixture, http.MethodPut, path, nil, true)
		if path == "/api/v1/downloads" {
			if req.Code != http.StatusMethodNotAllowed {
				t.Fatalf("download PUT = %d", req.Code)
			}
			continue
		}
		if req.Code != http.StatusBadRequest {
			t.Fatalf("token empty PUT = %d", req.Code)
		}
	}

	request := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads", map[string]string{}, true)
	if request.Code != http.StatusBadGateway {
		t.Fatalf("empty download body status = %d", request.Code)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads/missing/cancel", nil, true).Code; got != http.StatusNotFound {
		t.Fatalf("missing cancel status = %d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads/missing/retry", nil, true).Code; got != http.StatusNotFound {
		t.Fatalf("missing retry status = %d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads/missing", nil, true).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("download item method status = %d", got)
	}
}
