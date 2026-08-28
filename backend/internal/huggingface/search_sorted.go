package huggingface

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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

type DiscoverySearchPage struct {
	Items      []DiscoveryModel `json:"items"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// SearchSorted exposes the discovery ordering and metadata supported by the
// Hugging Face model API without leaking provider-specific field names into the
// management API or frontend.
func (c *Client) SearchSorted(ctx context.Context, opts SearchOptions) ([]DiscoveryModel, error) {
	page, err := c.SearchSortedPage(ctx, opts, "")
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// SearchSortedPage returns one provider-backed page plus the opaque cursor for
// the next page. The cursor is deliberately kept provider-specific and is only
// intended to be handed back to a subsequent call unchanged.
func (c *Client) SearchSortedPage(ctx context.Context, opts SearchOptions, cursor string) (DiscoverySearchPage, error) {
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
	if value := strings.TrimSpace(cursor); value != "" {
		q.Set("cursor", value)
	}
	q.Set("sort", discoverySort(opts.Sort))
	q.Set("direction", "-1")

	var raw []discoveryRawModel
	headers, err := c.getDiscoveryJSON(ctx, "/api/models?"+q.Encode(), &raw)
	if err != nil {
		return DiscoverySearchPage{}, err
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
	return DiscoverySearchPage{Items: out, NextCursor: nextCursorFromLink(headers.Get("Link"))}, nil
}

func (c *Client) getDiscoveryJSON(ctx context.Context, endpoint string, dst any) (http.Header, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	resolved := c.baseURL.ResolveReference(u)
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, resolved.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.token != nil {
		token, err := c.token(ctx)
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Hugging Face request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("Hugging Face returned HTTP %d: %s", resp.StatusCode, message)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dst); err != nil {
		return nil, fmt.Errorf("decode Hugging Face response: %w", err)
	}
	return resp.Header.Clone(), nil
}

func nextCursorFromLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) && !strings.Contains(part, "rel=next") {
			continue
		}
		start := strings.IndexByte(part, '<')
		end := strings.IndexByte(part, '>')
		if start < 0 || end <= start+1 {
			continue
		}
		next, err := url.Parse(part[start+1 : end])
		if err != nil {
			continue
		}
		if cursor := strings.TrimSpace(next.Query().Get("cursor")); cursor != "" {
			return cursor
		}
	}
	return ""
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
