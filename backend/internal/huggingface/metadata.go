package huggingface

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/brantje/llamarack/backend/internal/ggufmeta"
)

const (
	discoveryMetadataLimit          = int64(8 << 20)
	discoveryMetadataMaxLimit       = int64(50_000_000)
	discoveryMetadataCandidateLimit = 3
)

var discoveryMetadataCache sync.Map

// DerivedMetadata enriches the Hugging Face model-info GGUF summary with only
// the low-level architecture dimensions needed by the KV-cache estimator. The
// Hub remains authoritative for architecture and context capability; the
// bounded GGUF read supplies block/embedding/head dimensions that model-info
// does not expose today.
//
// Repositories frequently contain several GGUF roles (target, draft, vision,
// projector) and may expose a large sharded BF16 artifact before the practical
// inference quantizations. Metadata is model-wide, so prefer a compact,
// single-file target quantization and retry a small number of alternatives when
// one artifact is auxiliary, malformed, or otherwise lacks target dimensions.
//
// Most files fit in the initial 8 MiB range. If a large or interleaved tokenizer
// payload pushes the required dimensions beyond that range, retry once with the
// same 50 MB safety ceiling used by Hugging Face's remote GGUF parser. Tensor
// descriptors and tensor data are never parsed.
func (c *Client) DerivedMetadata(ctx context.Context, detail ModelDetail) (ggufmeta.Derived, error) {
	candidates := discoveryMetadataCandidates(detail.Artifacts)
	if len(candidates) == 0 {
		return providerDerived(ggufmeta.Derived{}, detail.GGUF), fmt.Errorf("GGUF metadata unavailable: no complete model artifact")
	}

	var lastDerived ggufmeta.Derived
	var lastErr error
	for _, modelFile := range candidates {
		if err := ctx.Err(); err != nil {
			return providerDerived(lastDerived, detail.GGUF), err
		}
		cacheKey := c.baseURL.String() + "|" + detail.ID + "|" + detail.Revision + "|" + modelFile
		if cached, ok := discoveryMetadataCache.Load(cacheKey); ok {
			return providerDerived(cached.(ggufmeta.Derived), detail.GGUF), nil
		}

		rawURL, err := c.DownloadURL(detail.ID, detail.Revision, modelFile)
		if err != nil {
			lastErr = err
			continue
		}

		for attempt, limit := range []int64{discoveryMetadataLimit, discoveryMetadataMaxLimit} {
			derived, inspectErr := c.readDerivedMetadataRange(ctx, rawURL, limit)
			lastDerived = derived
			if inspectErr == nil {
				discoveryMetadataCache.Store(cacheKey, derived)
				return providerDerived(derived, detail.GGUF), nil
			}
			lastErr = fmt.Errorf("%s: %w", modelFile, inspectErr)
			if attempt == 0 && metadataRangeExhausted(inspectErr) {
				continue
			}
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no metadata candidate succeeded")
	}
	return providerDerived(lastDerived, detail.GGUF), fmt.Errorf("GGUF metadata unavailable after %d candidate artifacts: %w", len(candidates), lastErr)
}

type discoveryMetadataCandidate struct {
	path  string
	score int
	size  int64
}

func discoveryMetadataCandidates(artifacts []Artifact) []string {
	candidates := make([]discoveryMetadataCandidate, 0, len(artifacts))
	seen := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		if !artifact.Complete || len(artifact.Files) == 0 {
			continue
		}
		modelFile := strings.TrimSpace(artifact.Files[0].Path)
		if modelFile == "" {
			continue
		}
		if _, ok := seen[modelFile]; ok {
			continue
		}
		seen[modelFile] = struct{}{}
		candidates = append(candidates, discoveryMetadataCandidate{
			path: modelFile, score: discoveryMetadataCandidateScore(artifact), size: artifact.ModelBytes,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		if candidates[i].size != candidates[j].size {
			return candidates[i].size < candidates[j].size
		}
		return candidates[i].path < candidates[j].path
	})
	if len(candidates) > discoveryMetadataCandidateLimit {
		candidates = candidates[:discoveryMetadataCandidateLimit]
	}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.path)
	}
	return out
}

func discoveryMetadataCandidateScore(artifact Artifact) int {
	quantization := strings.ToUpper(strings.TrimSpace(artifact.Quantization))
	score := 50
	switch quantization {
	case "Q4_K_M":
		score = 0
	case "Q4_K_S", "IQ4_XS", "IQ4_NL", "Q4_0", "Q4_1":
		score = 5
	case "Q5_K_M", "Q5_K_S", "Q5_K_L":
		score = 10
	case "Q6_K", "Q6_K_L", "Q8_0":
		score = 15
	case "F16", "BF16", "F32":
		score = 40
	default:
		if strings.HasPrefix(quantization, "Q4") || strings.HasPrefix(quantization, "IQ4") {
			score = 6
		} else if strings.HasPrefix(quantization, "Q5") {
			score = 11
		} else if strings.HasPrefix(quantization, "Q6") || strings.HasPrefix(quantization, "Q8") {
			score = 16
		}
	}
	if artifact.ShardCount > 1 || artifact.ExpectedShards > 1 {
		score += 20
	}
	name := strings.ToLower(artifact.Name)
	if hasArtifactRoleToken(name, "draft") || hasArtifactRoleToken(name, "vision") {
		score += 100
	}
	return score
}

func hasArtifactRoleToken(name, token string) bool {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".gguf")
	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/'
	}) {
		if part == token {
			return true
		}
	}
	return false
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
