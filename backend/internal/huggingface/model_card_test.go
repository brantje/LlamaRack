package huggingface

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetailWithCardEnrichesCarniceStyleModelCard(t *testing.T) {
	readme := `---
license: apache-2.0
base_model: kai-os/Carnice-V3
library_name: gguf
pipeline_tag: image-text-to-text
language:
  - en
  - nl
tags: [qwen3.8, tool-use]
---
# Carnice-V3 GGUF for Hermes Agent

> **Important limitations:** quantization does not repair the behavioral limitations of the source model.

This repository contains high-quality GGUF quantizations of the complete merged BF16 [Carnice-V3](https://huggingface.co/kai-os/Carnice-V3) checkpoint.

Carnice V3 is based on ` + "`Qwen/Qwen3.8-27B`" + ` and is intended for agent workloads.

## Files and recommendations

This must not become the repository description.
`
	readmeRequests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models/kai-os/Carnice-V3-GGUF":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":        "kai-os/Carnice-V3-GGUF",
				"author":    "kai-os",
				"sha":       "rev-card",
				"downloads": 577,
				"likes":     20,
				"tags":      []string{"gguf", "license:apache-2.0", "base_model:quantized:kai-os/Carnice-V3"},
				"siblings":  []map[string]any{{"rfilename": "Carnice-V3-Q5_K_M.gguf", "size": 100}},
			})
		case "/kai-os/Carnice-V3-GGUF/raw/rev-card/README.md":
			readmeRequests++
			_, _ = w.Write([]byte(readme))
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	client, err := NewClientWithHTTP(provider.URL, nil, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		detail, err := client.DetailWithCard(context.Background(), "kai-os/Carnice-V3-GGUF")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(detail.Description, "Important limitations") || !strings.Contains(detail.Description, "high-quality GGUF quantizations") {
			t.Fatalf("description=%q", detail.Description)
		}
		if strings.Contains(detail.Description, "Files and recommendations") || strings.Contains(detail.Description, "https://huggingface.co") || strings.Contains(detail.Description, "**") {
			t.Fatalf("description was not cleaned/bounded to the intro: %q", detail.Description)
		}
		if detail.CardMetadata == nil {
			t.Fatal("card metadata missing")
		}
		metadata := detail.CardMetadata
		if metadata.License != "apache-2.0" || metadata.PipelineTag != "image-text-to-text" || metadata.LibraryName != "gguf" {
			t.Fatalf("metadata=%+v", metadata)
		}
		if strings.Join(metadata.BaseModels, ",") != "kai-os/Carnice-V3" || strings.Join(metadata.Languages, ",") != "en,nl" {
			t.Fatalf("metadata=%+v", metadata)
		}
	}
	if readmeRequests != 1 {
		t.Fatalf("expected revision-scoped model-card cache, requests=%d", readmeRequests)
	}
}

func TestDetailWithCardKeepsProviderDescriptionWhenREADMEUnavailable(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models/acme/demo" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "acme/demo", "sha": "rev-no-readme",
			"cardData": map[string]any{"description": "Provider description"},
			"tags":     []string{"license:mit", "base_model:finetune:acme/base", "base_model:finetune:acme/base"},
		})
	}))
	defer provider.Close()
	client, err := NewClientWithHTTP(provider.URL, nil, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	detail, err := client.DetailWithCard(context.Background(), "acme/demo")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Description != "Provider description" || detail.CardMetadata == nil || detail.CardMetadata.License != "mit" {
		t.Fatalf("detail=%+v", detail)
	}
	if got := strings.Join(detail.CardMetadata.BaseModels, ","); got != "acme/base" {
		t.Fatalf("base models=%q", got)
	}
}

func TestModelCardHelpersCoverFrontMatterAndIntroEdges(t *testing.T) {
	values, content := splitModelCardFrontMatter("\ufeff---\r\nlicense: 'apache-2.0'\r\nlanguage: [en, \"fr\"]\r\nbase_model:\r\n  - acme/base\r\n  ignored: nested\r\n# comment\r\n---\r\n# Title\r\nHello")
	if firstMetadataValue(values, "license") != "apache-2.0" || strings.Join(values["language"], ",") != "en,fr" || strings.Join(values["base_model"], ",") != "acme/base" || !strings.Contains(content, "Hello") {
		t.Fatalf("values=%v content=%q", values, content)
	}
	if values, content := splitModelCardFrontMatter("# No front matter\nText"); len(values) != 0 || !strings.Contains(content, "Text") {
		t.Fatalf("unexpected no-frontmatter parse: %v %q", values, content)
	}
	if values, content := splitModelCardFrontMatter("---\nlicense: mit\nnever closes"); len(values) != 0 || !strings.Contains(content, "license: mit") {
		t.Fatalf("unexpected unterminated parse: %v %q", values, content)
	}
	if got := firstMetadataValue(map[string][]string{"x": {"", " value "}}, "x"); got != "value" {
		t.Fatalf("first=%q", got)
	}
	if got := modelCardDescription("# Title\n<!-- hidden -->\n![hero](hero.png)\n> [Useful](https://example.test) **text** with <b>HTML</b>.\n\n## Next\nignored", ""); got != "Useful text with HTML." {
		t.Fatalf("description=%q", got)
	}
	if got := modelCardDescription("ignored", " **Front matter** `description` "); got != "Front matter description" {
		t.Fatalf("front description=%q", got)
	}
	if got := truncateDescription(strings.Repeat("x", maxModelDescriptionSize+20)); len(got) <= maxModelDescriptionSize || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated len=%d suffix=%v", len(got), strings.HasSuffix(got, "…"))
	}
}

func TestModelCardEnrichmentInputAndHTTPFailures(t *testing.T) {
	provider := httptest.NewServer(http.NotFoundHandler())
	defer provider.Close()
	client, err := NewClientWithHTTP(provider.URL, nil, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := client.modelCardEnrichment(context.Background(), "bad", "rev"); err != nil || got.Description != "" {
		t.Fatalf("invalid repo=%+v err=%v", got, err)
	}
	if got, err := client.modelCardEnrichment(context.Background(), "acme/demo", ""); err != nil || got.Description != "" {
		t.Fatalf("empty revision=%+v err=%v", got, err)
	}
	if _, err := client.modelCardEnrichment(context.Background(), "acme/demo", "missing"); err == nil {
		t.Fatal("expected HTTP failure")
	}
	if _, err := client.DetailWithCard(context.Background(), "missing/repo"); err == nil {
		t.Fatal("expected detail failure")
	}
}

func TestMergeModelCardMetadataAndEmpty(t *testing.T) {
	metadata := mergeModelCardMetadata(ModelCardMetadata{
		BaseModels: []string{"acme/base", "ACME/base", ""}, Languages: []string{"en", "EN", ""},
	}, []string{"license:bsd-3-clause", "base_model:quantized:ignored/base"})
	if metadata.License != "bsd-3-clause" || len(metadata.BaseModels) != 1 || len(metadata.Languages) != 1 || metadata.empty() {
		t.Fatalf("metadata=%+v", metadata)
	}
	if !(ModelCardMetadata{}).empty() {
		t.Fatal("zero metadata should be empty")
	}
}
