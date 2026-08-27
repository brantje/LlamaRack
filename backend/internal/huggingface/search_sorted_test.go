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
		{"", "trending_score"},
		{"trending_score", "trending_score"},
		{"trending", "trending_score"},
		{"likes", "likes"},
		{"downloads", "downloads"},
		{"created_at", "created_at"},
		{"createdAt", "created_at"},
		{"last_modified", "last_modified"},
		{"lastModified", "last_modified"},
		{"unexpected", "trending_score"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			items, err := client.SearchSorted(context.Background(), SearchOptions{
				Query: "demo", Author: "acme", Sort: tc.input, Limit: 150,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].ID != "acme/demo" {
				t.Fatalf("items = %+v", items)
			}
			if got := seen.Get("sort"); got != tc.want {
				t.Fatalf("sort = %q, want %q", got, tc.want)
			}
			if seen.Get("direction") != "-1" || seen.Get("filter") != "gguf" || seen.Get("full") != "true" {
				t.Fatalf("query = %v", seen)
			}
			if seen.Get("search") != "demo" || seen.Get("author") != "acme" || seen.Get("limit") != "100" {
				t.Fatalf("query = %v", seen)
			}
		})
	}
}

func TestSearchSortedDefaultsLimitAndSkipsEmptyIDs(t *testing.T) {
	var seen url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query()
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": ""},
			{"modelId": "acme/fallback", "downloads": 1, "likes": 2},
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
	if len(items) != 1 || items[0].ID != "acme/fallback" || items[0].Author != "acme" {
		t.Fatalf("items = %+v", items)
	}
}
