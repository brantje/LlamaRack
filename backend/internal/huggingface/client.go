package huggingface

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type TokenProvider func(context.Context) (string, error)

type Client struct {
	baseURL *url.URL
	http    *http.Client
	token   TokenProvider
}

type SearchOptions struct {
	Query  string
	Author string
	Sort   string
	Limit  int
}

type ModelSummary struct {
	ID           string   `json:"id"`
	Author       string   `json:"author,omitempty"`
	Downloads    int64    `json:"downloads"`
	Likes        int64    `json:"likes"`
	LastModified string   `json:"last_modified,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Private      bool     `json:"private"`
	Gated        bool     `json:"gated"`
}

type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	OID  string `json:"oid,omitempty"`
}

type Artifact struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Quantization   string `json:"quantization,omitempty"`
	TotalBytes     int64  `json:"total_bytes"`
	ShardCount     int    `json:"shard_count"`
	ExpectedShards int    `json:"expected_shards"`
	Complete       bool   `json:"complete"`
	Files          []File `json:"files"`
}

type ModelDetail struct {
	ID           string     `json:"id"`
	Author       string     `json:"author,omitempty"`
	Description  string     `json:"description,omitempty"`
	Downloads    int64      `json:"downloads"`
	Likes        int64      `json:"likes"`
	LastModified string     `json:"last_modified,omitempty"`
	Tags         []string   `json:"tags,omitempty"`
	Private      bool       `json:"private"`
	Gated        bool       `json:"gated"`
	Revision     string     `json:"revision"`
	Artifacts    []Artifact `json:"artifacts"`
}

type rawModel struct {
	ID           string          `json:"id"`
	ModelID      string          `json:"modelId"`
	Author       string          `json:"author"`
	Downloads    int64           `json:"downloads"`
	Likes        int64           `json:"likes"`
	LastModified string          `json:"lastModified"`
	Tags         []string        `json:"tags"`
	Private      bool            `json:"private"`
	Gated        json.RawMessage `json:"gated"`
	SHA          string          `json:"sha"`
	CardData     struct {
		Description string `json:"description"`
	} `json:"cardData"`
	Siblings []struct {
		Filename string `json:"rfilename"`
		Size     int64  `json:"size"`
		BlobID   string `json:"blobId"`
		LFS      *struct {
			OID  string `json:"oid"`
			Size int64  `json:"size"`
		} `json:"lfs"`
	} `json:"siblings"`
}

var (
	shardPattern = regexp.MustCompile(`(?i)^(.*)-(\d{5})-of-(\d{5})\.gguf$`)
	quantPattern = regexp.MustCompile(`(?i)(?:^|[-_.])(IQ\d(?:_[A-Z0-9]+)+|Q\d(?:_[A-Z0-9]+)+|BF16|F16|F32)(?:[-_.]|$)`)
)

func NewClient(base string, token TokenProvider) (*Client, error) {
	if strings.TrimSpace(base) == "" {
		base = "https://huggingface.co"
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("invalid Hugging Face base URL")
	}
	c := &Client{baseURL: u, token: token}
	c.http = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many Hugging Face redirects")
			}
			if !strings.EqualFold(req.URL.Host, c.baseURL.Host) {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}
	return c, nil
}

func NewClientWithHTTP(base string, token TokenProvider, client *http.Client) (*Client, error) {
	c, err := NewClient(base, token)
	if err != nil {
		return nil, err
	}
	if client != nil {
		c.http = client
	}
	return c, nil
}

func (c *Client) Search(ctx context.Context, opts SearchOptions) ([]ModelSummary, error) {
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
	switch opts.Sort {
	case "likes", "lastModified":
		q.Set("sort", opts.Sort)
	default:
		q.Set("sort", "downloads")
	}
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

func (c *Client) Detail(ctx context.Context, repoID string) (ModelDetail, error) {
	repoID = strings.Trim(strings.TrimSpace(repoID), "/")
	if !validRepoID(repoID) {
		return ModelDetail{}, errors.New("invalid Hugging Face repository id")
	}
	var raw rawModel
	endpoint := "/api/models/" + escapeRepo(repoID) + "?blobs=true"
	if err := c.getJSON(ctx, endpoint, &raw); err != nil {
		return ModelDetail{}, err
	}
	id := firstNonEmpty(raw.ID, raw.ModelID, repoID)
	files := make([]File, 0, len(raw.Siblings))
	for _, sibling := range raw.Siblings {
		if !strings.EqualFold(path.Ext(sibling.Filename), ".gguf") {
			continue
		}
		size := sibling.Size
		oid := sibling.BlobID
		if sibling.LFS != nil {
			if sibling.LFS.Size > 0 {
				size = sibling.LFS.Size
			}
			if sibling.LFS.OID != "" {
				oid = sibling.LFS.OID
			}
		}
		files = append(files, File{Path: sibling.Filename, Size: size, OID: oid})
	}
	return ModelDetail{
		ID: id, Author: firstNonEmpty(raw.Author, repoAuthor(id)), Description: strings.TrimSpace(raw.CardData.Description),
		Downloads: raw.Downloads, Likes: raw.Likes, LastModified: raw.LastModified, Tags: raw.Tags,
		Private: raw.Private, Gated: rawGated(raw.Gated), Revision: raw.SHA, Artifacts: GroupArtifacts(id, raw.SHA, files),
	}, nil
}

func (c *Client) DownloadURL(repoID, revision, filename string) (string, error) {
	if !validRepoID(repoID) || strings.TrimSpace(revision) == "" || !validProviderPath(filename) {
		return "", errors.New("invalid Hugging Face artifact identity")
	}
	base := strings.TrimSuffix(c.baseURL.String(), "/")
	return base + "/" + escapeRepo(repoID) + "/resolve/" + url.PathEscape(revision) + "/" + escapePath(filename), nil
}

func (c *Client) NewDownloadRequest(ctx context.Context, method, rawURL string) (*http.Request, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || !sameHost(c.baseURL, u) {
		return nil, errors.New("refusing non-Hugging Face download URL")
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
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
	return req, nil
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.http.Do(req)
}

func GroupArtifacts(repoID, revision string, files []File) []Artifact {
	type group struct {
		name     string
		expected int
		files    []File
	}
	groups := map[string]*group{}
	for _, file := range files {
		if !strings.EqualFold(path.Ext(file.Path), ".gguf") {
			continue
		}
		base := path.Base(file.Path)
		key := file.Path
		expected := 1
		name := base
		if match := shardPattern.FindStringSubmatch(base); match != nil {
			expected, _ = strconv.Atoi(match[3])
			dir := path.Dir(file.Path)
			logical := match[1] + ".gguf"
			if dir != "." {
				key = dir + "/" + logical
			} else {
				key = logical
			}
			name = logical
		}
		g := groups[key]
		if g == nil {
			g = &group{name: name, expected: expected}
			groups[key] = g
		}
		if expected > g.expected {
			g.expected = expected
		}
		g.files = append(g.files, file)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Artifact, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		sort.Slice(g.files, func(i, j int) bool { return g.files[i].Path < g.files[j].Path })
		var total int64
		for _, file := range g.files {
			total += file.Size
		}
		expected := g.expected
		if expected <= 0 {
			expected = len(g.files)
		}
		out = append(out, Artifact{
			ID: artifactID(repoID, revision, key), Name: g.name, Quantization: detectQuantization(g.name),
			TotalBytes: total, ShardCount: len(g.files), ExpectedShards: expected,
			Complete: len(g.files) == expected, Files: append([]File(nil), g.files...),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Quantization == out[j].Quantization {
			return out[i].Name < out[j].Name
		}
		return out[i].Quantization < out[j].Quantization
	})
	return out
}

func (c *Client) getJSON(ctx context.Context, endpoint string, dst any) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	resolved := c.baseURL.ResolveReference(u)
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, resolved.String(), nil)
	if err != nil {
		return err
	}
	if c.token != nil {
		token, err := c.token(ctx)
		if err != nil {
			return err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Hugging Face request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("Hugging Face returned HTTP %d: %s", resp.StatusCode, message)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dst); err != nil {
		return fmt.Errorf("decode Hugging Face response: %w", err)
	}
	return nil
}

func artifactID(repoID, revision, key string) string {
	sum := sha256.Sum256([]byte(repoID + "\x00" + revision + "\x00" + key))
	return hex.EncodeToString(sum[:8])
}

func detectQuantization(name string) string {
	match := quantPattern.FindStringSubmatch(strings.ToUpper(name))
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func rawGated(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" || string(raw) == `"false"` {
		return false
	}
	return true
}

func repoAuthor(repoID string) string {
	if index := strings.IndexByte(repoID, '/'); index > 0 {
		return repoID[:index]
	}
	return ""
}

func validRepoID(repoID string) bool {
	parts := strings.Split(repoID, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != "" && parts[0] != "." && parts[1] != "." && !strings.Contains(repoID, "..")
}

func validProviderPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != "." && !strings.HasPrefix(cleaned, "../")
}

func escapeRepo(repoID string) string {
	parts := strings.Split(repoID, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func escapePath(value string) string {
	parts := strings.Split(value, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func sameHost(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
