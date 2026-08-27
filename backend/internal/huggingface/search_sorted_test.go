package huggingface

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSearchSortedUsesDiscoveryOrdering(t *testing.T) {
	var seen url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query()
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": "acme/demo", "downloads": 12, "likes": 3, "tags": []string{"gguf"},
			"gguf": map[string]any{"total": 27_000_000_000},
		}})
	}))
	defer server.Close()
	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		input string
		want  string
	}{
		{"", "trendingScore"},
		{"trending_score", "trendingScore"},
		{"trending", "trendingScore"},
		{"likes", "likes"},
		{"downloads", "downloads"},
		{"created_at", "createdAt"},
		{"createdAt", "createdAt"},
		{"last_modified", "lastModified"},
		{"lastModified", "lastModified"},
		{"unexpected", "trendingScore"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			items, err := client.SearchSorted(context.Background(), SearchOptions{
				Query: "demo", Author: "acme", Sort: tc.input, Limit: 150,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].ID != "acme/demo" || items[0].ParameterCount != 27_000_000_000 {
				t.Fatalf("items = %+v", items)
			}
			if got := seen.Get("sort"); got != tc.want {
				t.Fatalf("sort = %q, want %q", got, tc.want)
			}
			if seen.Get("direction") != "-1" || seen.Get("filter") != "gguf" || seen.Has("full") {
				t.Fatalf("query = %v", seen)
			}
			if len(seen["expand[]"]) == 0 {
				t.Fatalf("missing discovery metadata expansion: %v", seen)
			}
			if seen.Get("search") != "demo" || seen.Get("author") != "acme" || seen.Get("limit") != "100" {
				t.Fatalf("query = %v", seen)
			}
		})
	}
}

func TestSearchSortedDefaultsLimitSkipsEmptyIDsAndFallsBackToSafetensorsParameters(t *testing.T) {
	var seen url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query()
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": ""},
			{"modelId": "acme/fallback", "downloads": 1, "likes": 2, "safetensors": map[string]any{"parameters": map[string]any{"F16": 10, "BF16": 20}}},
		})
	}))
	defer server.Close()
	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.SearchSorted(context.Background(), SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Get("limit") != "24" || seen.Has("search") || seen.Has("author") {
		t.Fatalf("query = %v", seen)
	}
	if seen.Get("sort") != "trendingScore" {
		t.Fatalf("default sort = %q", seen.Get("sort"))
	}
	if len(items) != 1 || items[0].ID != "acme/fallback" || items[0].Author != "acme" || items[0].ParameterCount != 30 {
		t.Fatalf("items = %+v", items)
	}
}

func TestParameterCountSkipsEmptyMetadata(t *testing.T) {
	if got := parameterCount(nil, &parameterInfo{}, &parameterInfo{Parameters: map[string]int64{"none": 0}}); got != 0 {
		t.Fatalf("parameter count = %d", got)
	}
}
