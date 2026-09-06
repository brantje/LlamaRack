package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/brantje/llamarack/backend/internal/instances"
)

func TestInstanceSlugRoutePreservesBoundaryOnLifecycleAndOptionFailures(t *testing.T) {
	ctx := context.Background()
	f := newAPIFixture(t, nil)
	model := createModel(t, f, nil)
	enabled := true
	instance, err := f.server.lifecycle.Instances().Create(ctx, instances.CreateInput{
		ModelID: model.ID,
		Name:    "Boundary Instance",
		Slug:    "boundary-instance",
		Enabled: &enabled,
		Options: map[string]string{"ctx-size": "2048"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if instance.ID == instance.Slug {
		t.Fatalf("test requires separate durable and public identities: %+v", instance)
	}

	for _, operation := range []string{"start", "restart"} {
		w := doRequest(t, f.server, http.MethodPost, "/api/v1/instances/"+instance.Slug+"/"+operation, nil, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s failure=%d body=%s", operation, w.Code, w.Body.String())
		}
	}

	f.dbExec(`DROP TABLE instance_options`)
	options := doRequest(t, f.server, http.MethodGet, "/api/v1/instances/"+instance.Slug+"/options", nil, nil)
	if options.Code != http.StatusInternalServerError || !strings.Contains(strings.ToLower(options.Body.String()), "instance_options") {
		t.Fatalf("options failure=%d body=%s", options.Code, options.Body.String())
	}
	duplicate := doRequest(t, f.server, http.MethodPost, "/api/v1/instances/"+instance.Slug+"/duplicate", nil, nil)
	if duplicate.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(duplicate.Body.String()), "instance_options") {
		t.Fatalf("duplicate failure=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
}
