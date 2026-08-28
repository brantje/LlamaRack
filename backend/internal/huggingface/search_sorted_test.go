package huggingface

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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

func TestSearchSortedPageReturnsAndAcceptsProviderCursor(t *testing.T) {
	var seenCursor string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCursor = r.URL.Query().Get("cursor")
		w.Header().Set("Link", "</api/models?cursor=next-token>; rel=next")
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "acme/demo"}})
	}))
	defer server.Close()
	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	page, err := client.SearchSortedPage(context.Background(), SearchOptions{Limit: 30}, "current-token")
	if err != nil {
		t.Fatal(err)
	}
	if seenCursor != "current-token" {
		t.Fatalf("cursor = %q", seenCursor)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "acme/demo" {
		t.Fatalf("items = %+v", page.Items)
	}
	if page.NextCursor != "next-token" {
		t.Fatalf("next cursor = %q", page.NextCursor)
	}
}

func TestSearchSortedPropagatesProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := NewClientWithHTTP(server.URL, nil, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	items, err := client.SearchSorted(context.Background(), SearchOptions{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("search error = %v", err)
	}
	if items != nil {
		t.Fatalf("items = %+v, want nil", items)
	}
}

func TestDiscoveryJSONErrors(t *testing.T) {
	t.Run("invalid endpoint", func(t *testing.T) {
		client, err := NewClient("https://huggingface.co", nil)
		if err != nil {
			t.Fatal(err)
		}
		var dst any
		if _, err := client.getDiscoveryJSON(context.Background(), "://bad", &dst); err == nil {
			t.Fatal("expected invalid endpoint error")
		}
	})

	t.Run("token provider", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("request should not be sent when token lookup fails")
		}))
		defer server.Close()
		client, err := NewClientWithHTTP(server.URL, func(context.Context) (string, error) {
			return "", errors.New("token unavailable")
		}, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		var dst any
		if _, err := client.getDiscoveryJSON(context.Background(), "/api/models", &dst); err == nil || !strings.Contains(err.Error(), "token unavailable") {
			t.Fatalf("token error = %v", err)
		}
	})

	t.Run("authorization header", func(t *testing.T) {
		var authorization string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authorization = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		}))
		defer server.Close()
		client, err := NewClientWithHTTP(server.URL, func(context.Context) (string, error) {
			return "secret-token", nil
		}, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		var dst []map[string]any
		if _, err := client.getDiscoveryJSON(context.Background(), "/api/models", &dst); err != nil {
			t.Fatal(err)
		}
		if authorization != "Bearer secret-token" {
			t.Fatalf("authorization = %q", authorization)
		}
	})

	t.Run("transport", func(t *testing.T) {
		httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport unavailable")
		})}
		client, err := NewClientWithHTTP("https://huggingface.co", nil, httpClient)
		if err != nil {
			t.Fatal(err)
		}
		var dst any
		if _, err := client.getDiscoveryJSON(context.Background(), "/api/models", &dst); err == nil || !strings.Contains(err.Error(), "Hugging Face request failed") || !strings.Contains(err.Error(), "transport unavailable") {
			t.Fatalf("transport error = %v", err)
		}
	})

	t.Run("provider status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("upstream exploded"))
		}))
		defer server.Close()
		client, err := NewClientWithHTTP(server.URL, nil, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		var dst any
		if _, err := client.getDiscoveryJSON(context.Background(), "/api/models", &dst); err == nil || !strings.Contains(err.Error(), "HTTP 502") || !strings.Contains(err.Error(), "upstream exploded") {
			t.Fatalf("status error = %v", err)
		}
	})

	t.Run("provider status without body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()
		client, err := NewClientWithHTTP(server.URL, nil, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		var dst any
		if _, err := client.getDiscoveryJSON(context.Background(), "/api/models", &dst); err == nil || !strings.Contains(err.Error(), "Service Unavailable") {
			t.Fatalf("status error = %v", err)
		}
	})

	t.Run("decode", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not-json"))
		}))
		defer server.Close()
		client, err := NewClientWithHTTP(server.URL, nil, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		var dst any
		if _, err := client.getDiscoveryJSON(context.Background(), "/api/models", &dst); err == nil || !strings.Contains(err.Error(), "decode Hugging Face response") {
			t.Fatalf("decode error = %v", err)
		}
	})
}

func TestNextCursorFromLink(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"<https://huggingface.co/api/models?cursor=abc123&limit=30>; rel=next", "abc123"},
		{"<https://huggingface.co/api/models?cursor=prev>; rel=prev, <https://huggingface.co/api/models?cursor=next>; rel=next", "next"},
		{"<https://huggingface.co/api/models?limit=30>; rel=next", ""},
		{"not-a-link; rel=next", ""},
		{"<://bad>; rel=next", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := nextCursorFromLink(tc.header); got != tc.want {
			t.Fatalf("nextCursorFromLink(%q) = %q, want %q", tc.header, got, tc.want)
		}
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

func TestParameterCountFallbacks(t *testing.T) {
	if got := parameterCount(nil, &parameterInfo{}, &parameterInfo{Parameters: map[string]int64{"none": 0}}); got != 0 {
		t.Fatalf("parameter count = %d", got)
	}
	if got := parameterCount(&parameterInfo{Total: 42}); got != 42 {
		t.Fatalf("total fallback = %d, want 42", got)
	}
}
