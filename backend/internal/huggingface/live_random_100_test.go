package huggingface

import (
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/hardware"
	"github.com/brantje/llamacpp-manager/backend/internal/recommendations"
)

// TestLiveRandom100GGUFModels is an intentionally temporary provider-integration
// sweep for issue #54. It samples 100 public, ungated GGUF repositories from a
// larger live Hugging Face population and exercises the exact Detail -> provider
// GGUF metadata -> bounded low-level enrichment -> Discover recommendation path.
func TestLiveRandom100GGUFModels(t *testing.T) {
	client, err := NewClient("https://huggingface.co", nil)
	if err != nil {
		t.Fatal(err)
	}

	population := make(map[string]DiscoveryModel)
	for _, sortMode := range []string{"trending_score", "downloads", "created_at", "last_modified"} {
		cursor := ""
		for pageNo := 0; pageNo < 2; pageNo++ {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			page, pageErr := client.SearchSortedPage(ctx, SearchOptions{Sort: sortMode, Limit: 100}, cursor)
			cancel()
			if pageErr != nil {
				t.Fatalf("build live population sort=%s page=%d: %v", sortMode, pageNo+1, pageErr)
			}
			for _, model := range page.Items {
				if model.ID == "" || model.Private || model.Gated {
					continue
				}
				population[model.ID] = model
			}
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
		}
	}
	if len(population) < 100 {
		t.Fatalf("live GGUF population too small: %d", len(population))
	}

	ids := make([]string, 0, len(population))
	for id := range population {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rng := rand.New(rand.NewSource(20260828))
	rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	ids = ids[:100]

	gib := int64(1024 * 1024 * 1024)
	snapshot := hardware.Snapshot{
		RAMAvailableBytes: 128 * gib,
		RAMTotalBytes:     128 * gib,
		GPUs: []hardware.GPU{
			{ID: "CUDA0", FreeBytes: 16 * gib, TotalBytes: 16 * gib},
			{ID: "CUDA1", FreeBytes: 16 * gib, TotalBytes: 16 * gib},
		},
	}

	type stats struct {
		detailOK, artifacts, providerGGUF, providerParams, providerArchitecture, providerContext int
		mixedProfiles, derivedOK, derivedFailed, metadataReady, recommendations, noRecommendation int
		multipleRecommendations, detailFailed, emptyArtifacts, profileFailures int
	}
	var s stats
	var hardFailures []string
	var enrichmentFailures []string
	var providerMissing []string

	for index, id := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		detail, detailErr := client.Detail(ctx, id)
		if detailErr != nil {
			cancel()
			s.detailFailed++
			hardFailures = append(hardFailures, fmt.Sprintf("%s: detail: %v", id, detailErr))
			t.Logf("%03d FAIL detail %-70s %v", index+1, id, detailErr)
			continue
		}
		s.detailOK++
		if len(detail.Artifacts) == 0 {
			cancel()
			s.emptyArtifacts++
			hardFailures = append(hardFailures, id+": no GGUF artifacts after detail grouping")
			t.Logf("%03d FAIL empty  %-70s", index+1, id)
			continue
		}
		s.artifacts += len(detail.Artifacts)

		if detail.GGUF != nil {
			s.providerGGUF++
			if detail.ParameterCount > 0 {
				s.providerParams++
			}
			if strings.TrimSpace(detail.GGUF.Architecture) != "" {
				s.providerArchitecture++
			}
			if detail.GGUF.ContextLength > 0 {
				s.providerContext++
			}
		} else {
			providerMissing = append(providerMissing, id+": gguf object absent")
		}

		for _, artifact := range detail.Artifacts {
			if artifact.Quantization == "" && artifact.BitsPerWeight > 0 {
				s.mixedProfiles++
				if artifact.ProfileQuantization() == "" {
					s.profileFailures++
				hardFailures = append(hardFailures, id+": BPW exists but mixed profile is empty")
				}
			}
		}

		derived, derivedErr := client.DerivedMetadata(ctx, detail)
		if derivedErr != nil {
			s.derivedFailed++
			enrichmentFailures = append(enrichmentFailures, fmt.Sprintf("%s: %v", id, derivedErr))
		} else {
			s.derivedOK++
		}
		cancel()

		metadata := recommendations.Metadata{
			Architecture: derived.Architecture,
			ContextLength: derived.ContextLength,
			BlockCount: derived.BlockCount,
			Embedding: derived.Embedding,
			HeadCount: derived.HeadCount,
			KVHeadCount: derived.KVHeadCount,
			KeyLength: derived.KeyLength,
			ValueLength: derived.ValueLength,
		}
		if metadata.BlockCount > 0 && metadata.Embedding > 0 && metadata.HeadCount > 0 {
			s.metadataReady++
		}
		inputs := make([]recommendations.ArtifactInput, 0, len(detail.Artifacts))
		for _, artifact := range detail.Artifacts {
			inputs = append(inputs, recommendations.ArtifactInput{
				ID: artifact.ID, Quantization: artifact.ProfileQuantization(), WeightsBytes: artifact.ModelBytes, Complete: artifact.Complete,
			})
		}
		analysis := recommendations.AnalyzeDiscover(inputs, metadata, derivedErr, snapshot, 4096, nil, true)
		recommended := 0
		for _, artifact := range analysis.Artifacts {
			if artifact.Recommended {
				recommended++
			}
		}
		if recommended == 1 {
			s.recommendations++
		} else if recommended > 1 {
			s.multipleRecommendations++
			hardFailures = append(hardFailures, fmt.Sprintf("%s: %d recommended artifacts", id, recommended))
		} else {
			s.noRecommendation++
		}

		providerSummary := "missing"
		if detail.GGUF != nil {
			providerSummary = fmt.Sprintf("params=%d arch=%q ctx=%d", detail.ParameterCount, detail.GGUF.Architecture, detail.GGUF.ContextLength)
		}
		t.Logf("%03d %-4s %-70s artifacts=%d provider=[%s] derivedErr=%v meta=%d/%d/%d recommended=%d", index+1, "OK", id, len(detail.Artifacts), providerSummary, derivedErr, derived.BlockCount, derived.Embedding, derived.HeadCount, recommended)
	}

	t.Logf("RANDOM100 SUMMARY population=%d detail_ok=%d detail_failed=%d empty_artifacts=%d artifacts=%d provider_gguf=%d provider_params=%d provider_arch=%d provider_ctx=%d mixed_profiles=%d profile_failures=%d derived_ok=%d derived_failed=%d metadata_ready=%d recommended=%d no_recommendation=%d multiple_recommendations=%d",
		len(population), s.detailOK, s.detailFailed, s.emptyArtifacts, s.artifacts, s.providerGGUF, s.providerParams, s.providerArchitecture, s.providerContext, s.mixedProfiles, s.profileFailures, s.derivedOK, s.derivedFailed, s.metadataReady, s.recommendations, s.noRecommendation, s.multipleRecommendations)
	if len(providerMissing) > 0 {
		t.Logf("PROVIDER MISSING (%d): %s", len(providerMissing), strings.Join(providerMissing, " | "))
	}
	if len(enrichmentFailures) > 0 {
		t.Logf("ENRICHMENT FAILURES (%d): %s", len(enrichmentFailures), strings.Join(enrichmentFailures, " | "))
	}
	if len(hardFailures) > 0 {
		t.Logf("HARD FAILURES (%d): %s", len(hardFailures), strings.Join(hardFailures, " | "))
	}

	// Deliberately fail this temporary audit so non-verbose CI emits every t.Logf
	// line. The file is removed after the report has been collected.
	t.Fatalf("LIVE RANDOM100 AUDIT COMPLETE: hard=%d provider_missing=%d enrichment_failures=%d multiple_recommendations=%d", len(hardFailures), len(providerMissing), len(enrichmentFailures), s.multipleRecommendations)
}
