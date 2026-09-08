package main

import (
	"net/http"
	"testing"
)

func TestBenchmarkOpenAPIOperations(t *testing.T) {
	doc := newOpenAPIDocument()
	registerBenchmarkOpenAPIOperations(doc)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/benchmarks"},
		{http.MethodGet, "/api/v1/benchmarks/capabilities"},
		{http.MethodPost, "/api/v1/instances/{id}/benchmarks"},
		{http.MethodGet, "/api/v1/benchmarks/{id}"},
		{http.MethodPost, "/api/v1/benchmarks/{id}/cancel"},
		{http.MethodDelete, "/api/v1/benchmarks/{id}"},
	} {
		if !doc.HasOperation(route.method, route.path) {
			t.Fatalf("missing benchmark OpenAPI operation %s %s", route.method, route.path)
		}
	}
	create := doc.Paths["/api/v1/instances/{id}/benchmarks"]["post"]
	if create.Security == nil || create.RequestBody == nil || len(create.Parameters) != 1 || create.Parameters[0].Name != "id" {
		t.Fatalf("benchmark create contract=%+v", create)
	}
}
