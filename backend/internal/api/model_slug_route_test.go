package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/brantje/llamarack/backend/internal/models"
)

func TestModelManagementSlugRenameKeepsDurableID(t *testing.T) {
	f := newAPIFixture(t, nil)
	created := createModel(t, f, nil)
	if created.Slug != "api-model" {
		t.Fatalf("created slug=%q", created.Slug)
	}

	bySlug := doRequest(t, f.server, http.MethodGet, "/api/v1/models/api-model", nil, nil)
	if bySlug.Code != http.StatusOK {
		t.Fatalf("GET by slug=%d body=%s", bySlug.Code, bySlug.Body.String())
	}

	body := map[string]any{
		"name": "API Model", "slug": "API Model Renamed", "context_length": created.ContextLength,
	}
	unconfirmed := doRequest(t, f.server, http.MethodPut, "/api/v1/models/api-model", body, nil)
	if unconfirmed.Code != http.StatusConflict {
		t.Fatalf("unconfirmed slug rename=%d body=%s", unconfirmed.Code, unconfirmed.Body.String())
	}

	body["confirm_slug_change"] = true
	renamedResponse := doRequest(t, f.server, http.MethodPut, "/api/v1/models/api-model", body, nil)
	if renamedResponse.Code != http.StatusOK {
		t.Fatalf("confirmed slug rename=%d body=%s", renamedResponse.Code, renamedResponse.Body.String())
	}
	var renamed models.Model
	if err := json.Unmarshal(renamedResponse.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.ID != created.ID || renamed.Slug != "api-model-renamed" {
		t.Fatalf("rename changed durable identity: before=%+v after=%+v", created, renamed)
	}

	if oldSlug := doRequest(t, f.server, http.MethodGet, "/api/v1/models/api-model", nil, nil); oldSlug.Code != http.StatusNotFound {
		t.Fatalf("old slug remained canonical: status=%d body=%s", oldSlug.Code, oldSlug.Body.String())
	}
	if newSlug := doRequest(t, f.server, http.MethodGet, "/api/v1/models/api-model-renamed", nil, nil); newSlug.Code != http.StatusOK {
		t.Fatalf("new slug route=%d body=%s", newSlug.Code, newSlug.Body.String())
	}
	// Transitional opaque-ID management links remain compatible, but frontend
	// navigation uses the new slug as the canonical route.
	if byID := doRequest(t, f.server, http.MethodGet, "/api/v1/models/"+created.ID, nil, nil); byID.Code != http.StatusOK {
		t.Fatalf("legacy opaque-ID route=%d body=%s", byID.Code, byID.Body.String())
	}
}
