package huggingface

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

type parameterInfo struct {
	Total      int64            `json:"total"`
	Parameters map[string]int64 `json:"parameters"`
}

type discoveryRawModel struct {
	rawModel
	GGUF        *parameterInfo `json:"gguf"`
	Safetensors *parameterInfo `json:"safetensors"`
}

type DiscoveryModel struct {
	ModelSummary
	ParameterCount int64 `json:"parameter_count,omitempty"`
}

// SearchSorted exposes the discovery ordering and metadata supported by the
// Hugging Face model API without leaking provider-specific field names into the
// management API or frontend.
func (c *Client) SearchSorted(ctx context.Context, opts SearchOptions) ([]DiscoveryModel, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 24
	}
	if limit > 100 {
		limit = 100
	}

	q := url.Values{}
	q.Set("filter", "gguf")
	q.Set("limit", strconv.Itoa(limit))
	for _, field := range []string{"author", "downloads", "likes", "lastModified", "tags", "private", "gated", "gguf", "safetensors"} {
		q.Add("expand[]", field)
	}
	if value := strings.TrimSpace(opts.Query); value != "" {
		q.Set("search", value)
	}
	if value := strings.TrimSpace(opts.Author); value != "" {
		q.Set("author", value)
	}
	q.Set("sort", discoverySort(opts.Sort))
	q.Set("direction", "-1")

	var raw []discoveryRawModel
	if err := c.getJSON(ctx, "/api/models?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	out := make([]DiscoveryModel, 0, len(raw))
	for _, item := range raw {
		id := firstNonEmpty(item.ID, item.ModelID)
		if id == "" {
			continue
		}
		out = append(out, DiscoveryModel{
			ModelSummary: ModelSummary{
				ID: id, Author: firstNonEmpty(item.Author, repoAuthor(id)), Downloads: item.Downloads,
				Likes: item.Likes, LastModified: item.LastModified, Tags: item.Tags,
				Private: item.Private, Gated: rawGated(item.Gated),
			},
			ParameterCount: parameterCount(item.GGUF, item.Safetensors),
		})
	}
	return out, nil
}

func parameterCount(values ...*parameterInfo) int64 {
	for _, value := range values {
		if value == nil {
			continue
		}
		var total int64
		for _, count := range value.Parameters {
			if count > 0 {
				total += count
			}
		}
		if total > 0 {
			return total
		}
		if value.Total > 0 {
			return value.Total
		}
	}
	return 0
}

func discoverySort(value string) string {
	switch strings.TrimSpace(value) {
	case "likes":
		return "likes"
	case "downloads":
		return "downloads"
	case "created_at", "createdAt":
		return "createdAt"
	case "last_modified", "lastModified":
		return "lastModified"
	case "trending_score", "trending", "":
		return "trendingScore"
	default:
		return "trendingScore"
	}
}
