package huggingface

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
)

const discoveryMetadataLimit = int64(8 << 20)

var discoveryMetadataCache sync.Map

// DerivedMetadata reads the small GGUF metadata prefix from one model artifact.
// It never downloads tensor data. Successful results are cached by immutable
// Hugging Face revision so moving the Discover context control stays local and
// responsive after the first inspection.
func (c *Client) DerivedMetadata(ctx context.Context, detail ModelDetail) (ggufmeta.Derived, error) {
	var modelFile string
	for _, artifact := range detail.Artifacts {
		if !artifact.Complete || len(artifact.Files) == 0 {
			continue
		}
		modelFile = strings.TrimSpace(artifact.Files[0].Path)
		if modelFile != "" {
			break
		}
	}
	if modelFile == "" {
		return ggufmeta.Derived{}, fmt.Errorf("GGUF metadata unavailable: no complete model artifact")
	}
	cacheKey := c.baseURL.String() + "|" + detail.ID + "|" + detail.Revision + "|" + modelFile
	if cached, ok := discoveryMetadataCache.Load(cacheKey); ok {
		return cached.(ggufmeta.Derived), nil
	}

	rawURL, err := c.DownloadURL(detail.ID, detail.Revision, modelFile)
	if err != nil {
		return ggufmeta.Derived{}, err
	}
	req, err := c.NewDownloadRequest(ctx, http.MethodGet, rawURL)
	if err != nil {
		return ggufmeta.Derived{}, err
	}
	// Architecture metadata lives at the beginning of a GGUF. The range is a
	// safety ceiling; InspectDerivedReader intentionally stops before tokenizer
	// tables, and closing the body stops any larger response when a proxy ignores
	// Range.
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", discoveryMetadataLimit-1))
	resp, err := c.http.Do(req)
	if err != nil {
		return ggufmeta.Derived{}, fmt.Errorf("read Hugging Face GGUF metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return ggufmeta.Derived{}, fmt.Errorf("GGUF metadata request returned HTTP %d", resp.StatusCode)
	}
	derived, err := ggufmeta.InspectDerivedReader(io.LimitReader(resp.Body, discoveryMetadataLimit))
	if err != nil {
		return derived, err
	}
	discoveryMetadataCache.Store(cacheKey, derived)
	return derived, nil
}
