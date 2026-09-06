package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/instances"
	"github.com/brantje/llamarack/backend/internal/models"
)

func TestIdentityRouteMethodValidationEdges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		model  bool
		method string
		parts  []string
		want   int
		ok     bool
	}{
		{name: "model collection method", model: true, method: http.MethodPatch, parts: []string{"slug"}, want: http.StatusMethodNotAllowed},
		{name: "model nested depth", model: true, method: http.MethodGet, parts: []string{"slug", "runtime", "extra"}, want: http.StatusNotFound},
		{name: "model unknown nested", model: true, method: http.MethodGet, parts: []string{"slug", "wat"}, want: http.StatusNotFound},
		{name: "model nested wrong method", model: true, method: http.MethodPost, parts: []string{"slug", "runtime"}, want: http.StatusMethodNotAllowed},
		{name: "instance collection method", method: http.MethodPatch, parts: []string{"slug"}, want: http.StatusMethodNotAllowed},
		{name: "instance bad stream shape", method: http.MethodGet, parts: []string{"slug", "runtime", "stream"}, want: http.StatusNotFound},
		{name: "instance stream wrong method", method: http.MethodPost, parts: []string{"slug", "logs", "stream"}, want: http.StatusMethodNotAllowed},
		{name: "instance nested depth", method: http.MethodGet, parts: []string{"slug", "runtime", "extra", "more"}, want: http.StatusNotFound},
		{name: "instance unknown nested", method: http.MethodGet, parts: []string{"slug", "wat"}, want: http.StatusNotFound},
		{name: "instance nested wrong method", method: http.MethodPost, parts: []string{"slug", "options"}, want: http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			var ok bool
			if tc.model {
				ok = validModelRouteMethod(w, tc.method, tc.parts)
			} else {
				ok = validInstanceRouteMethod(w, tc.method, tc.parts)
			}
			if ok != tc.ok || w.Code != tc.want {
				t.Fatalf("ok=%v code=%d want ok=%v code=%d", ok, w.Code, tc.ok, tc.want)
			}
		})
	}
}

func TestModelSlugUpdateValidationEdges(t *testing.T) {
	ctx := context.Background()
	f := newAPIFixture(t, nil)
	first := createModel(t, f, nil)

	secondPath := filepath.Join(f.dir, "second-Q4_K_M.gguf")
	if err := os.WriteFile(secondPath, []byte("gguf-two"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := f.models.Create(ctx, models.CreateModelInput{Name: "Second Model", Slug: "occupied-model", GGUFPath: secondPath})
	if err != nil {
		t.Fatal(err)
	}
	if second.Slug != "occupied-model" {
		t.Fatalf("second=%+v", second)
	}

	invalidJSON := doRequest(t, f.server, http.MethodPut, "/api/v1/models/"+first.Slug, map[string]any{
		"name": first.Name, "unexpected": true,
	}, nil)
	if invalidJSON.Code != http.StatusBadRequest {
		t.Fatalf("unknown update field=%d body=%s", invalidJSON.Code, invalidJSON.Body.String())
	}

	invalidOption := doRequest(t, f.server, http.MethodPut, "/api/v1/models/"+first.Slug, map[string]any{
		"name": first.Name, "options": map[string]string{"definitely-not-a-llama-option": "1"},
	}, nil)
	if invalidOption.Code != http.StatusBadRequest {
		t.Fatalf("invalid option=%d body=%s", invalidOption.Code, invalidOption.Body.String())
	}

	missingName := doRequest(t, f.server, http.MethodPut, "/api/v1/models/"+first.Slug, map[string]any{
		"name": "",
	}, nil)
	if missingName.Code != http.StatusBadRequest {
		t.Fatalf("missing name=%d body=%s", missingName.Code, missingName.Body.String())
	}

	collision := doRequest(t, f.server, http.MethodPut, "/api/v1/models/"+first.Slug, map[string]any{
		"name": first.Name, "slug": second.Slug, "confirm_slug_change": true,
	}, nil)
	if collision.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(collision.Body.String()), "unique") {
		t.Fatalf("slug collision=%d body=%s", collision.Code, collision.Body.String())
	}
}

func TestInstanceSlugUpdateValidationEdges(t *testing.T) {
	ctx := context.Background()
	f := newAPIFixture(t, nil)
	model := createModel(t, f, nil)
	enabled := true
	first, err := f.server.lifecycle.Instances().Create(ctx, instances.CreateInput{ModelID: model.ID, Name: "First Instance", Slug: "first-instance", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.server.lifecycle.Instances().Create(ctx, instances.CreateInput{ModelID: model.ID, Name: "Second Instance", Slug: "occupied-instance", Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}

	invalidJSON := doRequest(t, f.server, http.MethodPut, "/api/v1/instances/"+first.Slug, map[string]any{
		"model_id": model.ID, "name": first.Name, "unexpected": true,
	}, nil)
	if invalidJSON.Code != http.StatusBadRequest {
		t.Fatalf("unknown update field=%d body=%s", invalidJSON.Code, invalidJSON.Body.String())
	}

	invalidOption := doRequest(t, f.server, http.MethodPut, "/api/v1/instances/"+first.Slug, map[string]any{
		"model_id": model.ID, "name": first.Name, "options": map[string]string{"definitely-not-a-llama-option": "1"},
	}, nil)
	if invalidOption.Code != http.StatusBadRequest {
		t.Fatalf("invalid option=%d body=%s", invalidOption.Code, invalidOption.Body.String())
	}

	unconfirmed := doRequest(t, f.server, http.MethodPut, "/api/v1/instances/"+first.Slug, map[string]any{
		"model_id": model.ID, "name": first.Name, "slug": "renamed-instance",
	}, nil)
	if unconfirmed.Code != http.StatusConflict {
		t.Fatalf("unconfirmed rename=%d body=%s", unconfirmed.Code, unconfirmed.Body.String())
	}

	collision := doRequest(t, f.server, http.MethodPut, "/api/v1/instances/"+first.Slug, map[string]any{
		"model_id": model.ID, "name": first.Name, "slug": second.Slug, "confirm_slug_change": true,
	}, nil)
	if collision.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(collision.Body.String()), "unique") {
		t.Fatalf("slug collision=%d body=%s", collision.Code, collision.Body.String())
	}

	if got, err := f.server.lifecycle.Instances().GetByID(ctx, first.ID); err != nil || got.Slug != first.Slug {
		t.Fatalf("failed update changed identity: got=%+v err=%v", got, err)
	}
}
