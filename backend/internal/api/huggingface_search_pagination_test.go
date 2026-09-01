package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/brantje/llamarack/backend/internal/huggingface"
)

func TestHuggingFaceSearchReturnsPaginatedResponse(t *testing.T) {
	fixture := newHuggingFaceFixture(t)
	w := huggingFaceRequest(t, fixture, http.MethodGet, "/api/v1/huggingface/search?q=demo&limit=5", nil, true)
	if w.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", w.Code, w.Body.String())
	}
	var page huggingface.DiscoverySearchPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("paginated response decode: %v body=%s", err, w.Body.String())
	}
	if len(page.Items) != 1 || page.Items[0].ID != "acme/demo" {
		t.Fatalf("paged items = %+v", page.Items)
	}
}
