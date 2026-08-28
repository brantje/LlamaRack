package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/brantje/llamacpp-manager/backend/internal/huggingface"
)

func TestPhase8SearchKeepsLegacyResponseAndSupportsPagedResponse(t *testing.T) {
	fixture := newPhase8Fixture(t)

	legacy := phase8Request(t, fixture, http.MethodGet, "/api/v1/huggingface/search?q=demo&limit=5", nil, true)
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy search status=%d body=%s", legacy.Code, legacy.Body.String())
	}
	var items []huggingface.DiscoveryModel
	if err := json.Unmarshal(legacy.Body.Bytes(), &items); err != nil {
		t.Fatalf("legacy response is no longer an array: %v body=%s", err, legacy.Body.String())
	}
	if len(items) != 1 || items[0].ID != "acme/demo" {
		t.Fatalf("legacy items = %+v", items)
	}

	paged := phase8Request(t, fixture, http.MethodGet, "/api/v1/huggingface/search?q=demo&limit=5&paged=true", nil, true)
	if paged.Code != http.StatusOK {
		t.Fatalf("paged search status=%d body=%s", paged.Code, paged.Body.String())
	}
	var page huggingface.DiscoverySearchPage
	if err := json.Unmarshal(paged.Body.Bytes(), &page); err != nil {
		t.Fatalf("paged response decode: %v body=%s", err, paged.Body.String())
	}
	if len(page.Items) != 1 || page.Items[0].ID != "acme/demo" {
		t.Fatalf("paged items = %+v", page.Items)
	}
}
