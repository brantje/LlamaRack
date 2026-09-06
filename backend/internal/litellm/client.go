package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var ErrStoreModelInDB = errors.New("LiteLLM proxy must have STORE_MODEL_IN_DB enabled")

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type ModelParams struct {
	Model             string `json:"model"`
	APIBase           string `json:"api_base"`
	APIKey            string `json:"api_key"`
	CustomLLMProvider string `json:"custom_llm_provider"`
}

type ModelInfo struct {
	ID                  string `json:"id,omitempty"`
	LlamaRackManaged    bool   `json:"llamarack_managed"`
	LlamaRackInstanceID string `json:"llamarack_instance_id"`
}

type ModelEntry struct {
	ModelName     string      `json:"model_name"`
	LiteLLMParams ModelParams `json:"litellm_params"`
	ModelInfo     ModelInfo   `json:"model_info"`
}

type modelInfoResponse struct {
	Data []ModelEntry `json:"data"`
}

func NewClient(proxyURL, apiKey string, httpClient *http.Client) (*Client, error) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil, errors.New("proxy URL is required")
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("proxy URL must use http or https")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(proxyURL, "/"), apiKey: strings.TrimSpace(apiKey), http: httpClient}, nil
}

func (c *Client) ListModels(ctx context.Context) ([]ModelEntry, error) {
	var payload modelInfoResponse
	if err := c.doJSON(ctx, http.MethodGet, "/model/info", nil, &payload); err != nil {
		return nil, err
	}
	return payload.Data, nil
}

func (c *Client) CreateModel(ctx context.Context, entry ModelEntry) error {
	return c.doJSON(ctx, http.MethodPost, "/model/new", entry, nil)
}

func (c *Client) UpdateModel(ctx context.Context, entry ModelEntry) error {
	return c.doJSON(ctx, http.MethodPost, "/model/update", entry, nil)
}

func (c *Client) DeleteModel(ctx context.Context, modelInfoID string) error {
	body := map[string]string{"id": strings.TrimSpace(modelInfoID)}
	return c.doJSON(ctx, http.MethodPost, "/model/delete", body, nil)
}

func (c *Client) TestConnection(ctx context.Context) error {
	_, err := c.ListModels(ctx)
	return err
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp.StatusCode, raw)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func parseAPIError(status int, raw []byte) error {
	text := strings.TrimSpace(string(raw))
	if strings.Contains(strings.ToUpper(text), "STORE_MODEL_IN_DB") {
		return ErrStoreModelInDB
	}
	if text == "" {
		return fmt.Errorf("litellm proxy returned HTTP %d", status)
	}
	return fmt.Errorf("litellm proxy returned HTTP %d: %s", status, sanitizeProxyErrorText(text))
}

const maxProxyErrorText = 512

var (
	apiKeyJSONPattern = regexp.MustCompile(`(?i)("api_key"\s*:\s*")[^"]*(")`)
	skTokenPattern    = regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`)
)

func sanitizeProxyErrorText(text string) string {
	text = apiKeyJSONPattern.ReplaceAllString(text, `${1}[redacted]$2`)
	text = skTokenPattern.ReplaceAllString(text, "sk-[redacted]")
	runes := []rune(text)
	if len(runes) > maxProxyErrorText {
		return string(runes[:maxProxyErrorText]) + "…"
	}
	return text
}

func IsManaged(entry ModelEntry) bool {
	return entry.ModelInfo.LlamaRackManaged
}

// BuildModelEntry retains the pre-#180 helper contract for internal callers and
// tests that still use one value for ownership and public model identity.
func BuildModelEntry(instanceID, apiBase, inferenceKey, litellmModelID string) ModelEntry {
	return BuildInstanceModelEntry(instanceID, instanceID, apiBase, inferenceKey, litellmModelID)
}

// BuildInstanceModelEntry separates LlamaRack's immutable Instance ownership ID
// from the mutable OpenAI/LiteLLM model name.
func BuildInstanceModelEntry(instanceID, instanceSlug, apiBase, inferenceKey, litellmModelID string) ModelEntry {
	entry := ModelEntry{
		ModelName: instanceSlug,
		LiteLLMParams: ModelParams{
			Model:             "openai/" + instanceSlug,
			APIBase:           apiBase,
			APIKey:            inferenceKey,
			CustomLLMProvider: "openai",
		},
		ModelInfo: ModelInfo{
			LlamaRackManaged:    true,
			LlamaRackInstanceID: instanceID,
		},
	}
	if litellmModelID != "" {
		entry.ModelInfo.ID = litellmModelID
	}
	return entry
}

func entryDrifted(entry ModelEntry, instanceID, apiBase, inferenceKey string) bool {
	return instanceEntryDrifted(entry, instanceID, instanceID, apiBase, inferenceKey)
}

func instanceEntryDrifted(entry ModelEntry, instanceID, instanceSlug, apiBase, inferenceKey string) bool {
	if entry.ModelName != instanceSlug {
		return true
	}
	if entry.LiteLLMParams.Model != "openai/"+instanceSlug {
		return true
	}
	if entry.LiteLLMParams.APIBase != apiBase {
		return true
	}
	if comparableRemoteAPIKey(entry.LiteLLMParams.APIKey) && entry.LiteLLMParams.APIKey != inferenceKey {
		return true
	}
	if entry.ModelInfo.LlamaRackInstanceID != instanceID {
		return true
	}
	return false
}

func comparableRemoteAPIKey(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if strings.Contains(value, "*") || strings.Contains(lower, "redact") {
		return false
	}
	return true
}
