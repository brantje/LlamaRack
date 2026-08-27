package huggingface

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestNewClientWithHTTPRejectsInvalidBase(t *testing.T) {
	if _, err := NewClientWithHTTP("ftp://huggingface.co", nil, http.DefaultClient); err == nil {
		t.Fatal("expected invalid base URL error")
	}
}

func TestNewDownloadRequestPropagatesTokenProviderError(t *testing.T) {
	client, err := NewClient("https://huggingface.co", func(context.Context) (string, error) {
		return "", errors.New("token unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.NewDownloadRequest(context.Background(), http.MethodGet, "https://huggingface.co/acme/demo/resolve/rev/demo.gguf"); err == nil {
		t.Fatal("expected token provider error")
	}
}

func TestNewDownloadRequestRejectsInvalidMethod(t *testing.T) {
	client, err := NewClient("https://huggingface.co", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.NewDownloadRequest(context.Background(), "bad\nmethod", "https://huggingface.co/acme/demo/resolve/rev/demo.gguf"); err == nil {
		t.Fatal("expected invalid HTTP method error")
	}
}
