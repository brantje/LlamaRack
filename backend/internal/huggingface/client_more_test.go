package huggingface

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchDefaultsAndModelIDFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "24" || r.URL.Query().Get("sort") != "downloads" || r.URL.Query().Get("direction") != "-1" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `[{"modelId":"org/model","gated":null}]`)
	}))
	defer server.Close()
	client, _ := NewClientWithHTTP(server.URL, nil, server.Client())
	items, err := client.Search(context.Background(), SearchOptions{Sort: "unsupported"})
	if err != nil || len(items) != 1 || items[0].ID != "org/model" || items[0].Gated {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestDetailSiblingFallbacksAndNestedSplit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"modelId":"org/model","sha":"rev","gated":"manual",
			"siblings":[
				{"rfilename":"weights/model-IQ3_XXS-00001-of-00002.gguf","size":2,"blobId":"blob1"},
				{"rfilename":"weights/model-IQ3_XXS-00002-of-00002.gguf","size":3,"blobId":"blob2"},
				{"rfilename":"model-F16.GGUF","size":4}
			]
		}`)
	}))
	defer server.Close()
	client, _ := NewClientWithHTTP(server.URL, nil, server.Client())
	detail, err := client.Detail(context.Background(), "org/model")
	if err != nil { t.Fatal(err) }
	if !detail.Gated || detail.Author != "org" || len(detail.Artifacts) != 2 { t.Fatalf("detail = %+v", detail) }
	foundSplit := false
	for _, item := range detail.Artifacts {
		if item.ShardCount == 2 {
			foundSplit = true
			if item.Name != "model-IQ3_XXS.gguf" || item.Quantization != "IQ3_XXS" || item.TotalBytes != 5 || !item.Complete {
				t.Fatalf("split = %+v", item)
			}
		}
	}
	if !foundSplit { t.Fatal("missing nested split") }
}

func TestGetJSONTransportErrorAndEmptyToken(t *testing.T) {
	transportErr := errors.New("offline")
	client, err := NewClientWithHTTP("http://provider.test", nil, &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		return nil, transportErr
	})})
	if err != nil { t.Fatal(err) }
	var out any
	if err := client.getJSON(context.Background(), "/api/models", &out); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("transport error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" { t.Errorf("unexpected authorization") }
		_, _ = io.WriteString(w, `[]`)
	}))
	defer server.Close()
	empty, _ := NewClientWithHTTP(server.URL, func(context.Context) (string, error) { return "", nil }, server.Client())
	if _, err := empty.Search(context.Background(), SearchOptions{}); err != nil { t.Fatal(err) }
}

func TestDownloadIdentityValidationAndHelpers(t *testing.T) {
	client, _ := NewClient("https://huggingface.co/base", nil)
	for _, tc := range []struct{ repo, rev, file string }{
		{"a/b", "", "x.gguf"}, {"a/b", "rev", ""}, {"a/b", "rev", "."}, {"a/b", "rev", "a/../x.gguf"},
	} {
		if _, err := client.DownloadURL(tc.repo, tc.rev, tc.file); err == nil { t.Fatalf("expected rejection for %+v", tc) }
	}
	url, err := client.DownloadURL("a/b", "rev 1", "folder/model F16.gguf")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(url, "/base/a/b/resolve/rev%201/folder/model%20F16.gguf") { t.Fatalf("url = %q", url) }
	if firstNonEmpty(" ", " second ", "third") != "second" || firstNonEmpty("", "") != "" { t.Fatal("firstNonEmpty") }
	if rawGated([]byte("null")) || rawGated([]byte(`"false"`)) || !rawGated([]byte("true")) { t.Fatal("rawGated") }
}

func TestRedirectLimit(t *testing.T) {
	server := httptest.NewServer(nil)
	defer server.Close()
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+r.URL.Path, http.StatusFound)
	})
	client, err := NewClient(server.URL, nil)
	if err != nil { t.Fatal(err) }
	req, err := client.NewDownloadRequest(context.Background(), http.MethodGet, server.URL+"/loop.gguf")
	if err != nil { t.Fatal(err) }
	if _, err := client.Do(req); err == nil || !strings.Contains(err.Error(), "too many Hugging Face redirects") {
		t.Fatalf("redirect error = %v", err)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)
func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
