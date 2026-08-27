package huggingface

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// SearchSorted mirrors Search but exposes the discovery ordering supported by
// Hugging Face's model API. Keeping the provider mapping here avoids leaking
// provider-specific sort names into API handlers or the frontend.
func (c *Client) SearchSorted(ctx context.Context, opts SearchOptions) ([]ModelSummary, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 24
	}
	if limit > 100 {
		limit = 100
	}

	q := url.Values{}
	q.Set("filter", "gguf")
	q.Set("full", "true")
	q.Set("limit", strconv.Itoa(limit))
	if value := strings.TrimSpace(opts.Query); value != "" {
		q.Set("search", value)
	}
	if value := strings.TrimSpace(opts.Author); value != "" {
		q.Set("author", value)
	}
	q.Set("sort", discoverySort(opts.Sort))
	q.Set("direction", "-1")

	var raw []rawModel
	if err := c.getJSON(ctx, "/api/models?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	out := make([]ModelSummary, 0, len(raw))
	for _, item := range raw {
		id := firstNonEmpty(item.ID, item.ModelID)
		if id == "" {
			continue
		}
		out = append(out, ModelSummary{
			ID: id, Author: firstNonEmpty(item.Author, repoAuthor(id)), Downloads: item.Downloads,
			Likes: item.Likes, LastModified: item.LastModified, Tags: item.Tags,
			Private: item.Private, Gated: rawGated(item.Gated),
		})
	}
	return out, nil
}

func discoverySort(value string) string {
	switch strings.TrimSpace(value) {
	case "likes":
		return "likes"
	case "downloads":
		return "downloads"
	case "created_at", "createdAt":
		return "created_at"
	case "last_modified", "lastModified":
		return "last_modified"
	case "trending_score", "trending", "":
		return "trending_score"
	default:
		return "trending_score"
	}
}
