package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHuggingFaceAdditionalMethodAndDecodeBranches(t *testing.T) {
	fixture := newHuggingFaceFixture(t)

	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/huggingface/model?repo=acme%2Fdemo", nil, true).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("detail POST status = %d", got)
	}
	if got := huggingFaceRequest(t, fixture, http.MethodPost, "/api/v1/downloads/missing/nope", nil, true).Code; got != http.StatusNotFound {
		t.Fatalf("unknown download action status = %d", got)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/downloads", strings.NewReader("{"))
	req.AddCookie(fixture.cookie)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	fixture.handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed download body status = %d", w.Code)
	}
}
