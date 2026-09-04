package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAdminSystemExposesBuildIdentity(t *testing.T) {
	f := newAdminFixture(t)
	w := doRequest(t, f.handler, http.MethodGet, "/api/v1/system", nil, f.cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("system=%d body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		Identity struct {
			Version string `json:"version"`
			Channel string `json:"channel"`
			Variant string `json:"variant"`
			LlamaCpp struct {
				Release string `json:"release"`
				Build   string `json:"build"`
			} `json:"llama_cpp"`
		} `json:"identity"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Identity.Version == "" || payload.Identity.Channel == "" || payload.Identity.Variant == "" {
		t.Fatalf("incomplete build identity: %+v", payload.Identity)
	}
}
