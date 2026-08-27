package huggingface

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchDetailAndDownloadRequests(t *testing.T) {
	var searchSeen, detailSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hf_secret" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/models":
			searchSeen = true
			if r.URL.Query().Get("filter") != "gguf" || r.URL.Query().Get("search") != "demo" || r.URL.Query().Get("author") != "acme" || r.URL.Query().Get("sort") != "likes" || r.URL.Query().Get("limit") != "100" {
				t.Fatalf("unexpected search query: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"modelId": "acme/demo", "downloads": 12, "likes": 3, "lastModified": "2026-08-27", "tags": []string{"gguf"}, "private": true, "gated": "auto",
			}, {"id": ""}})
		case "/api/models/acme/demo":
			detailSeen = true
			if r.URL.Query().Get("blobs") != "true" {
				t.Fatalf("missing blobs query")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "acme/demo", "downloads": 12, "likes": 3, "lastModified": "2026-08-27", "tags": []string{"gguf"}, "private": false, "gated": false, "sha": "abc123",
				"cardData": map[string]any{"description": " Demo model "},
				"siblings": []map[string]any{
					{"rfilename": "demo-Q4_K_M-00001-of-00002.gguf", "lfs": map[string]any{"oid": "oid1", "size": 3}},
					{"rfilename": "demo-Q4_K_M-00002-of-00002.gguf", "lfs": map[string]any{"oid": "oid2", "size": 4}},
					{"rfilename": "nested/demo Q8_0.gguf", "size": 8, "blobId": "blob"},
					{"rfilename": "README.md", "size": 1},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClientWithHTTP(server.URL, func(context.Context) (string, error) { return "hf_secret", nil }, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.Search(context.Background(), SearchOptions{Query: "demo", Author: "acme", Sort: "likes", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if !searchSeen || len(items) != 1 || items[0].ID != "acme/demo" || items[0].Author != "acme" || !items[0].Private || !items[0].Gated {
		t.Fatalf("unexpected search result: %+v", items)
	}

	detail, err := client.Detail(context.Background(), " /acme/demo/ ")
	if err != nil {
		t.Fatal(err)
	}
	if !detailSeen || detail.Description != "Demo model" || detail.Revision != "abc123" || detail.Author != "acme" || len(detail.Artifacts) != 2 {
		t.Fatalf("unexpected detail: %+v", detail)
	}
	var split, single Artifact
	for _, artifact := range detail.Artifacts {
		if artifact.ShardCount == 2 {
			split = artifact
		} else {
			single = artifact
		}
	}
	if !split.Complete || split.ExpectedShards != 2 || split.TotalBytes != 7 || split.Quantization != "Q4_K_M" {
		t.Fatalf("unexpected split artifact: %+v", split)
	}
	if single.TotalBytes != 8 || single.Quantization != "Q8_0" || single.Files[0].OID != "blob" {
		t.Fatalf("unexpected single artifact: %+v", single)
	}

	rawURL, err := client.DownloadURL("acme/demo", "abc123", "nested/demo Q8_0.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rawURL, "nested/demo%20Q8_0.gguf") || strings.Contains(rawURL, "%2520") {
		t.Fatalf("download URL = %q", rawURL)
	}
	req, err := client.NewDownloadRequest(context.Background(), http.MethodGet, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") != "Bearer hf_secret" {
		t.Fatalf("download authorization = %q", req.Header.Get("Authorization"))
	}
	if _, err := client.NewDownloadRequest(context.Background(), http.MethodGet, "https://example.com/model.gguf"); err == nil {
		t.Fatal("expected foreign host rejection")
	}
}

func TestClientDefaultsErrorsAndGrouping(t *testing.T) {
	if _, err := NewClient("ftp://example.com", nil); err == nil {
		t.Fatal("expected invalid base URL")
	}
	client, err := NewClient("", nil)
	if err != nil || client.baseURL.Host != "huggingface.co" {
		t.Fatalf("default client: %+v %v", client, err)
	}
	if _, err := client.Detail(context.Background(), "invalid"); err == nil {
		t.Fatal("expected invalid repo error")
	}
	if _, err := client.DownloadURL("invalid", "rev", "x.gguf"); err == nil {
		t.Fatal("expected invalid download identity")
	}
	if _, err := client.DownloadURL("a/b", "rev", "../x.gguf"); err == nil {
		t.Fatal("expected unsafe path rejection")
	}

	files := []File{
		{Path: "a-Q5_K_M-00001-of-00003.gguf", Size: 2},
		{Path: "a-Q5_K_M-00003-of-00003.gguf", Size: 4},
		{Path: "plain.gguf", Size: 7},
		{Path: "ignore.txt", Size: 9},
	}
	artifacts := GroupArtifacts("org/repo", "rev", files)
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %+v", artifacts)
	}
	var incomplete Artifact
	for _, artifact := range artifacts {
		if artifact.ExpectedShards == 3 {
			incomplete = artifact
		}
	}
	if incomplete.Complete || incomplete.ShardCount != 2 || incomplete.Quantization != "Q5_K_M" {
		t.Fatalf("incomplete split = %+v", incomplete)
	}
	if detectQuantization("model-BF16.gguf") != "BF16" || detectQuantization("plain.gguf") != "" {
		t.Fatal("unexpected quantization detection")
	}
	if rawGated(nil) || rawGated(json.RawMessage("false")) || !rawGated(json.RawMessage(`"manual"`)) {
		t.Fatal("unexpected gated parsing")
	}
	if repoAuthor("org/repo") != "org" || repoAuthor("repo") != "" {
		t.Fatal("unexpected author parsing")
	}
	if validRepoID("one") || validRepoID("../x") || !validRepoID("a/b") {
		t.Fatal("unexpected repo validation")
	}
	if validProviderPath("/x.gguf") || validProviderPath("a\\b.gguf") || !validProviderPath("a/b.gguf") {
		t.Fatal("unexpected path validation")
	}
}

func TestHTTPFailuresAndTokenFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("case") {
		case "invalid":
			_, _ = io.WriteString(w, "not json")
		case "empty":
			w.WriteHeader(http.StatusTooManyRequests)
		case "status":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "denied")
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	client, _ := NewClientWithHTTP(server.URL, nil, server.Client())
	var out any
	if err := client.getJSON(context.Background(), "/?case=invalid", &out); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("invalid JSON error = %v", err)
	}
	if err := client.getJSON(context.Background(), "/?case=status", &out); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("status error = %v", err)
	}
	if err := client.getJSON(context.Background(), "/?case=empty", &out); err == nil || !strings.Contains(err.Error(), "Too Many Requests") {
		t.Fatalf("empty status error = %v", err)
	}

	tokenClient, _ := NewClientWithHTTP(server.URL, func(context.Context) (string, error) { return "", io.ErrUnexpectedEOF }, server.Client())
	if _, err := tokenClient.Search(context.Background(), SearchOptions{}); err == nil {
		t.Fatal("expected token provider error")
	}
	if _, err := tokenClient.NewDownloadRequest(context.Background(), http.MethodGet, server.URL+"/x.gguf"); err == nil {
		t.Fatal("expected download token provider error")
	}
}

func TestRedirectDropsAuthorization(t *testing.T) {
	var redirectedAuth string
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	client, err := NewClient(source.URL, func(context.Context) (string, error) { return "secret", nil })
	if err != nil {
		t.Fatal(err)
	}
	req, err := client.NewDownloadRequest(context.Background(), http.MethodGet, source.URL+"/x.gguf")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if redirectedAuth != "" {
		t.Fatalf("authorization leaked to redirect host: %q", redirectedAuth)
	}
}
