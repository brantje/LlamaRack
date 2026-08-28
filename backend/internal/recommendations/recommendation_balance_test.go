package recommendations

import "testing"

func estimatedTPS(min, max float64) GenerationSpeedEstimate {
	return GenerationSpeedEstimate{
		Estimated:          true,
		MinTokensPerSecond: min,
		MaxTokensPerSecond: max,
	}
}

func recommendationArtifact(id, quantization string, minTPS, maxTPS float64, weights int64) DiscoverArtifact {
	return DiscoverArtifact{
		ArtifactID:               id,
		Quantization:             ClassifyQuantization(quantization),
		Runnable:                 true,
		Fit:                      FitGPU,
		EstimatedGenerationSpeed: estimatedTPS(minTPS, maxTPS),
		weightsBytes:             weights,
		complete:                 true,
	}
}

func recommendedArtifactID(artifacts []DiscoverArtifact) string {
	for _, artifact := range artifacts {
		if artifact.Recommended {
			return artifact.ArtifactID
		}
	}
	return ""
}

func TestDiscoverRecommendationUsesQualityPerformanceSweetSpot(t *testing.T) {
	// Mirrors the gpt-oss-20b example from a 4060 Ti: Q8/F16 estimates overlap
	// the Q6 range, so their tiny estimated speed differences are not meaningful
	// enough to justify moving above the practical Q6 quality band.
	artifacts := []DiscoverArtifact{
		recommendationArtifact("f16", "F16", 12, 17, 13_000_000_000),
		recommendationArtifact("q8", "Q8_0", 13, 19, 11_400_000_000),
		recommendationArtifact("q8-xl", "Q8_K_XL", 12, 17, 12_000_000_000),
		recommendationArtifact("q6-xl", "Q6_K_XL", 12, 18, 11_200_000_000),
		recommendationArtifact("q6", "Q6_K", 12, 18, 10_900_000_000),
	}

	markDiscoverRecommendation(artifacts, true)
	if got := recommendedArtifactID(artifacts); got != "q6" {
		t.Fatalf("recommended=%q artifacts=%+v", got, artifacts)
	}
}

func TestDiscoverRecommendationDropsQualityWhenSpeedIsClearlyBetter(t *testing.T) {
	artifacts := []DiscoverArtifact{
		recommendationArtifact("q8", "Q8_0", 13, 19, 12_000_000_000),
		recommendationArtifact("q6", "Q6_K", 12, 18, 10_000_000_000),
		recommendationArtifact("q5", "Q5_K_M", 17, 24, 8_000_000_000),
		recommendationArtifact("q4", "Q4_K_M", 28, 38, 7_000_000_000),
	}

	markDiscoverRecommendation(artifacts, true)
	if got := recommendedArtifactID(artifacts); got != "q4" {
		t.Fatalf("clearly faster Q4 should win, got=%q artifacts=%+v", got, artifacts)
	}
}

func TestDiscoverRecommendationPreservesQualityFallbackWithoutEstimates(t *testing.T) {
	artifacts := []DiscoverArtifact{
		recommendationArtifact("q6", "Q6_K", 12, 18, 10_000_000_000),
		recommendationArtifact("f16", "F16", 12, 17, 13_000_000_000),
	}
	for index := range artifacts {
		artifacts[index].EstimatedGenerationSpeed = GenerationSpeedEstimate{}
	}

	markDiscoverRecommendation(artifacts, true)
	if got := recommendedArtifactID(artifacts); got != "f16" {
		t.Fatalf("quality fallback=%q artifacts=%+v", got, artifacts)
	}
}

func TestDiscoverRecommendationPrefersMeasuredQ6OverUnestimatedFullPrecision(t *testing.T) {
	artifacts := []DiscoverArtifact{
		recommendationArtifact("q6", "Q6_K", 12, 18, 10_000_000_000),
		recommendationArtifact("f16", "F16", 0, 0, 13_000_000_000),
	}
	artifacts[1].EstimatedGenerationSpeed = GenerationSpeedEstimate{}

	markDiscoverRecommendation(artifacts, true)
	if got := recommendedArtifactID(artifacts); got != "q6" {
		t.Fatalf("measured practical choice=%q artifacts=%+v", got, artifacts)
	}
}

func TestDiscoverRecommendationDoesNotLetSingleMeasuredQ2DisplaceF16(t *testing.T) {
	artifacts := []DiscoverArtifact{
		recommendationArtifact("q2", "Q2_K", 35, 50, 4_000_000_000),
		recommendationArtifact("f16", "F16", 0, 0, 13_000_000_000),
	}
	artifacts[1].EstimatedGenerationSpeed = GenerationSpeedEstimate{}

	markDiscoverRecommendation(artifacts, true)
	if got := recommendedArtifactID(artifacts); got != "f16" {
		t.Fatalf("single low-quality estimate must not erase quality fallback: %q", got)
	}
}

func TestDiscoverRecommendationStillHonorsHybridPolicy(t *testing.T) {
	artifacts := []DiscoverArtifact{
		recommendationArtifact("q6-gpu", "Q6_K", 12, 18, 10_000_000_000),
		recommendationArtifact("q6-hybrid", "Q6_K_XL", 20, 30, 10_100_000_000),
	}
	artifacts[1].Fit = FitHybrid

	markDiscoverRecommendation(artifacts, false)
	if got := recommendedArtifactID(artifacts); got != "q6-gpu" {
		t.Fatalf("hybrid policy=%q artifacts=%+v", got, artifacts)
	}
}

func TestRecommendationBalanceHelpers(t *testing.T) {
	if usableGenerationEstimate(GenerationSpeedEstimate{}) {
		t.Fatal("empty estimate must not be usable")
	}
	if usableGenerationEstimate(estimatedTPS(10, 9)) {
		t.Fatal("inverted estimate must not be usable")
	}
	if selectDiscoverRecommendation(nil, nil) != -1 {
		t.Fatal("empty candidate list should return -1")
	}

	artifacts := []DiscoverArtifact{
		recommendationArtifact("q6", "Q6_K", 10, 15, 10),
		recommendationArtifact("q8", "Q8_0", 10, 15, 12),
		recommendationArtifact("f16", "F16", 10, 15, 13),
	}
	if got := recommendationQualityCeiling(artifacts, []int{0, 1, 2}); got != 65 {
		t.Fatalf("practical ceiling=%d", got)
	}
	if got := recommendationQualityCeiling(artifacts, []int{1, 2}); got != 80 {
		t.Fatalf("Q8 ceiling=%d", got)
	}
	if got := recommendationQualityCeiling(artifacts, []int{2}); got != 100 {
		t.Fatalf("full precision ceiling=%d", got)
	}
}
