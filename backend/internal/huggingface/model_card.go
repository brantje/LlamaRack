package huggingface

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

const (
	maxModelCardBytes       = 256 << 10
	maxModelDescriptionSize = 4000
)

// ModelCardMetadata contains stable, novice-useful model-card facts. It is
// deliberately small rather than mirroring Hugging Face's arbitrary YAML.
type ModelCardMetadata struct {
	License     string   `json:"license,omitempty"`
	PipelineTag string   `json:"pipeline_tag,omitempty"`
	LibraryName string   `json:"library_name,omitempty"`
	BaseModels  []string `json:"base_models,omitempty"`
	Languages   []string `json:"languages,omitempty"`
}

// ModelDetailWithCard is the Discover detail representation. The embedded
// ModelDetail keeps the existing API contract while the outer description
// replaces an empty cardData.description with the README introduction.
type ModelDetailWithCard struct {
	ModelDetail
	Description  string             `json:"description,omitempty"`
	CardMetadata *ModelCardMetadata `json:"card_metadata,omitempty"`
}

type modelCardEnrichment struct {
	Description string
	Metadata    ModelCardMetadata
}

var (
	modelCardCache       sync.Map
	modelCardLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\([^\)]+\)`)
	modelCardHTMLPattern = regexp.MustCompile(`<[^>]+>`)
)

// DetailWithCard augments the normal provider detail with lightweight README
// metadata. Model-card retrieval is best-effort: failure must never prevent a
// repository from being browsed or downloaded.
func (c *Client) DetailWithCard(ctx context.Context, repoID string) (ModelDetailWithCard, error) {
	detail, err := c.Detail(ctx, repoID)
	if err != nil {
		return ModelDetailWithCard{}, err
	}

	enrichment, _ := c.modelCardEnrichment(ctx, detail.ID, detail.Revision)
	description := strings.TrimSpace(detail.Description)
	if description == "" {
		description = enrichment.Description
	}
	metadata := mergeModelCardMetadata(enrichment.Metadata, detail.Tags)
	var metadataPtr *ModelCardMetadata
	if !metadata.empty() {
		metadataPtr = &metadata
	}
	return ModelDetailWithCard{
		ModelDetail:   detail,
		Description:   description,
		CardMetadata: metadataPtr,
	}, nil
}

func (c *Client) modelCardEnrichment(ctx context.Context, repoID, revision string) (modelCardEnrichment, error) {
	if !validRepoID(repoID) || strings.TrimSpace(revision) == "" {
		return modelCardEnrichment{}, nil
	}
	key := c.baseURL.String() + "|" + repoID + "|" + revision
	if cached, ok := modelCardCache.Load(key); ok {
		return cached.(modelCardEnrichment), nil
	}

	rawURL := strings.TrimSuffix(c.baseURL.String(), "/") + "/" + escapeRepo(repoID) + "/raw/" + url.PathEscape(revision) + "/README.md"
	req, err := c.NewDownloadRequest(ctx, http.MethodGet, rawURL)
	if err != nil {
		return modelCardEnrichment{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return modelCardEnrichment{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return modelCardEnrichment{}, fmt.Errorf("Hugging Face model card returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxModelCardBytes+1))
	if err != nil {
		return modelCardEnrichment{}, err
	}
	if len(body) > maxModelCardBytes {
		body = body[:maxModelCardBytes]
	}
	metadataValues, content := splitModelCardFrontMatter(string(body))
	enrichment := modelCardEnrichment{
		Description: modelCardDescription(content, firstMetadataValue(metadataValues, "description")),
		Metadata: ModelCardMetadata{
			License:     firstMetadataValue(metadataValues, "license"),
			PipelineTag: firstMetadataValue(metadataValues, "pipeline_tag"),
			LibraryName: firstMetadataValue(metadataValues, "library_name"),
			BaseModels:  append([]string(nil), metadataValues["base_model"]...),
			Languages:   append([]string(nil), metadataValues["language"]...),
		},
	}
	modelCardCache.Store(key, enrichment)
	return enrichment, nil
}

func splitModelCardFrontMatter(body string) (map[string][]string, string) {
	body = strings.TrimPrefix(strings.ReplaceAll(body, "\r\n", "\n"), "\ufeff")
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return map[string][]string{}, body
	}
	end := -1
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			end = index
			break
		}
	}
	if end < 0 {
		return map[string][]string{}, body
	}

	values := make(map[string][]string)
	currentKey := ""
	for _, line := range lines[1:end] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && currentKey != "" {
			if value := cleanMetadataValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))); value != "" {
				values[currentKey] = append(values[currentKey], value)
			}
			continue
		}
		if len(line) != len(strings.TrimLeft(line, " \t")) {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			currentKey = ""
			continue
		}
		currentKey = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			for _, item := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"), ",") {
				if cleaned := cleanMetadataValue(item); cleaned != "" {
					values[currentKey] = append(values[currentKey], cleaned)
				}
			}
			continue
		}
		if cleaned := cleanMetadataValue(value); cleaned != "" {
			values[currentKey] = append(values[currentKey], cleaned)
		}
	}
	return values, strings.Join(lines[end+1:], "\n")
}

func cleanMetadataValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
			value = value[1 : len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}

func firstMetadataValue(values map[string][]string, key string) string {
	for _, value := range values[key] {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func modelCardDescription(content, frontMatterDescription string) string {
	if value := strings.TrimSpace(frontMatterDescription); value != "" {
		return truncateDescription(cleanModelCardMarkdown(value))
	}
	lines := strings.Split(content, "\n")
	output := make([]string, 0, 12)
	seenTitle := false
	inComment := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<!--") {
			inComment = true
		}
		if inComment {
			if strings.Contains(trimmed, "-->") {
				inComment = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			seenTitle = true
			continue
		}
		if strings.HasPrefix(trimmed, "## ") && (seenTitle || len(output) > 0) {
			break
		}
		if strings.HasPrefix(trimmed, "![") || strings.HasPrefix(strings.ToLower(trimmed), "<img") {
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
		}
		cleaned := cleanModelCardMarkdown(trimmed)
		if cleaned == "" {
			if len(output) > 0 && output[len(output)-1] != "" {
				output = append(output, "")
			}
			continue
		}
		output = append(output, cleaned)
	}
	for len(output) > 0 && output[len(output)-1] == "" {
		output = output[:len(output)-1]
	}
	return truncateDescription(strings.TrimSpace(strings.Join(output, "\n")))
}

func cleanModelCardMarkdown(value string) string {
	value = modelCardLinkPattern.ReplaceAllString(value, "$1")
	value = modelCardHTMLPattern.ReplaceAllString(value, "")
	for _, marker := range []string{"**", "__", "`"} {
		value = strings.ReplaceAll(value, marker, "")
	}
	return strings.TrimSpace(value)
}

func truncateDescription(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxModelDescriptionSize {
		return value
	}
	return strings.TrimSpace(value[:maxModelDescriptionSize]) + "…"
}

func mergeModelCardMetadata(metadata ModelCardMetadata, tags []string) ModelCardMetadata {
	if metadata.License == "" {
		for _, tag := range tags {
			if strings.HasPrefix(strings.ToLower(tag), "license:") {
				metadata.License = strings.TrimSpace(tag[len("license:"):])
				break
			}
		}
	}
	if len(metadata.BaseModels) == 0 {
		for _, tag := range tags {
			if !strings.HasPrefix(strings.ToLower(tag), "base_model:") {
				continue
			}
			value := strings.TrimSpace(tag[len("base_model:"):])
			for _, relation := range []string{"quantized:", "finetune:", "adapter:", "merge:"} {
				if strings.HasPrefix(strings.ToLower(value), relation) {
					value = strings.TrimSpace(value[len(relation):])
					break
				}
			}
			if value != "" {
				metadata.BaseModels = append(metadata.BaseModels, value)
			}
		}
	}
	metadata.BaseModels = uniqueModelCardValues(metadata.BaseModels)
	metadata.Languages = uniqueModelCardValues(metadata.Languages)
	return metadata
}

func uniqueModelCardValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (m ModelCardMetadata) empty() bool {
	return m.License == "" && m.PipelineTag == "" && m.LibraryName == "" && len(m.BaseModels) == 0 && len(m.Languages) == 0
}
