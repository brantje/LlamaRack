package huggingface

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
)

const (
	discoveryMetadataLimit    = int64(8 << 20)
	discoveryMetadataMaxLimit = int64(50_000_000)
)

var discoveryMetadataCache sync.Map

// DerivedMetadata enriches the Hugging Face model-info GGUF summary with only
// the low-level architecture dimensions needed by the KV-cache estimator. The
// Hub remains authoritative for architecture and context capability; the
// bounded GGUF read supplies block/embedding/head dimensions that model-info
// does not expose today.
//
// Most files fit in the initial 8 MiB range. If a large or interleaved tokenizer
// payload pushes the required dimensions beyond that range, retry once with the
// same 50 MB safety ceiling used by Hugging Face's remote GGUF parser. Tensor
// descriptors and tensor data are never parsed.
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
		return providerDerived(ggufmeta.Derived{}, detail.GGUF), fmt.Errorf("GGUF metadata unavailable: no complete model artifact")
	}
	cacheKey := c.baseURL.String() + "|" + detail.ID + "|" + detail.Revision + "|" + modelFile
	if cached, ok := discoveryMetadataCache.Load(cacheKey); ok {
		return providerDerived(cached.(ggufmeta.Derived), detail.GGUF), nil
	}

	rawURL, err := c.DownloadURL(detail.ID, detail.Revision, modelFile)
	if err != nil {
		return providerDerived(ggufmeta.Derived{}, detail.GGUF), err
	}

	var lastDerived ggufmeta.Derived
	for attempt, limit := range []int64{discoveryMetadataLimit, discoveryMetadataMaxLimit} {
		derived, err := c.readDerivedMetadataRange(ctx, rawURL, limit)
		derived = providerDerived(derived, detail.GGUF)
		lastDerived = derived
		if err == nil {
			discoveryMetadataCache.Store(cacheKey, derived)
			return derived, nil
		}
		if attempt == 0 && metadataRangeExhausted(err) {
			continue
		}
		return derived, err
	}
	return lastDerived, errors.New("GGUF metadata unavailable")
}

func providerDerived(value ggufmeta.Derived, info *GGUFInfo) ggufmeta.Derived {
	if info == nil {
		return value
	}
	if architecture := strings.TrimSpace(info.Architecture); architecture != "" {
		value.Architecture = architecture
	}
	if info.ContextLength > 0 {
		value.ContextLength = info.ContextLength
	}
	return value
}

func (c *Client) readDerivedMetadataRange(ctx context.Context, rawURL string, limit int64) (ggufmeta.Derived, error) {
	req, err := c.NewDownloadRequest(ctx, http.MethodGet, rawURL)
	if err != nil {
		return ggufmeta.Derived{}, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", limit-1))
	resp, err := c.http.Do(req)
	if err != nil {
		return ggufmeta.Derived{}, fmt.Errorf("read Hugging Face GGUF metadata: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		return ggufmeta.Derived{}, fmt.Errorf("GGUF metadata request returned HTTP %d", resp.StatusCode)
	}
	derived, inspectErr := ggufmeta.InspectDerivedReader(io.LimitReader(resp.Body, limit))
	closeErr := resp.Body.Close()
	if inspectErr != nil {
		return derived, inspectErr
	}
	if closeErr != nil {
		return derived, fmt.Errorf("close Hugging Face GGUF metadata response: %w", closeErr)
	}
	return derived, nil
}

func metadataRangeExhausted(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
