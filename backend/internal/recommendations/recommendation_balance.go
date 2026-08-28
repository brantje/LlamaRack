package recommendations

// selectDiscoverRecommendation chooses a novice-friendly quality/performance
// sweet spot when hardware-derived generation estimates are available. The
// estimate is a range, so small differences whose ranges overlap are treated as
// indistinguishable instead of pretending that a 1 tok/s midpoint difference is
// meaningful.
func selectDiscoverRecommendation(artifacts []DiscoverArtifact, candidates []int) int {
	if len(candidates) == 0 {
		return -1
	}

	estimated := make([]int, 0, len(candidates))
	for _, index := range candidates {
		if usableGenerationEstimate(artifacts[index].EstimatedGenerationSpeed) {
			estimated = append(estimated, index)
		}
	}

	// With no performance data, preserve the previous quality-first behavior.
	if len(estimated) == 0 {
		return highestQualityCandidate(artifacts, candidates)
	}

	// A single measured low-quality artifact should not automatically displace a
	// substantially higher-quality choice whose speed simply could not be
	// estimated. Q4+ is a useful minimum before one measured candidate can drive
	// the recommendation by itself.
	if len(estimated) == 1 && artifacts[estimated[0]].Quantization.rank < 40 {
		return highestQualityCandidate(artifacts, candidates)
	}

	fastestFloor := 0.0
	for _, index := range estimated {
		if min := artifacts[index].EstimatedGenerationSpeed.MinTokensPerSecond; min > fastestFloor {
			fastestFloor = min
		}
	}

	// Keep every artifact whose estimated range still overlaps the strongest
	// conservative speed result. Anything below this set is clearly slower even
	// after accounting for estimate uncertainty.
	competitive := make([]int, 0, len(estimated))
	for _, index := range estimated {
		if artifacts[index].EstimatedGenerationSpeed.MaxTokensPerSecond >= fastestFloor {
			competitive = append(competitive, index)
		}
	}
	if len(competitive) == 0 {
		return highestQualityCandidate(artifacts, estimated)
	}

	// Q4-Q6 is the practical local-inference quality band: going to Q8 or
	// near-full precision normally buys diminishing quality returns. When one of
	// those practical choices is speed-competitive, prefer the highest-quality
	// member of that band. If Q4-Q6 is absent, Q8 becomes the practical ceiling;
	// full precision is the fallback only when no quantized high-quality option is
	// available.
	ceiling := recommendationQualityCeiling(artifacts, competitive)
	preferred := make([]int, 0, len(competitive))
	for _, index := range competitive {
		if artifacts[index].Quantization.rank <= ceiling {
			preferred = append(preferred, index)
		}
	}
	if len(preferred) == 0 {
		preferred = competitive
	}
	return highestQualityCandidate(artifacts, preferred)
}

func usableGenerationEstimate(estimate GenerationSpeedEstimate) bool {
	return estimate.Estimated && estimate.MinTokensPerSecond > 0 && estimate.MaxTokensPerSecond >= estimate.MinTokensPerSecond
}

func recommendationQualityCeiling(artifacts []DiscoverArtifact, candidates []int) int {
	hasPractical := false
	hasQ8Class := false
	for _, index := range candidates {
		rank := artifacts[index].Quantization.rank
		if rank >= 40 && rank <= 65 {
			hasPractical = true
		}
		if rank > 65 && rank <= 80 {
			hasQ8Class = true
		}
	}
	if hasPractical {
		return 65
	}
	if hasQ8Class {
		return 80
	}
	return 100
}

func highestQualityCandidate(artifacts []DiscoverArtifact, candidates []int) int {
	if len(candidates) == 0 {
		return -1
	}
	best := candidates[0]
	for _, index := range candidates[1:] {
		current, candidate := artifacts[best], artifacts[index]
		if candidate.Quantization.rank > current.Quantization.rank {
			best = index
			continue
		}
		if candidate.Quantization.rank < current.Quantization.rank {
			continue
		}

		// Same quality tier: prefer the stronger conservative generation estimate,
		// then the upper end of the range, then the smaller artifact.
		candidateEstimated := usableGenerationEstimate(candidate.EstimatedGenerationSpeed)
		currentEstimated := usableGenerationEstimate(current.EstimatedGenerationSpeed)
		if candidateEstimated != currentEstimated {
			if candidateEstimated {
				best = index
			}
			continue
		}
		if candidateEstimated {
			if candidate.EstimatedGenerationSpeed.MinTokensPerSecond > current.EstimatedGenerationSpeed.MinTokensPerSecond {
				best = index
				continue
			}
			if candidate.EstimatedGenerationSpeed.MinTokensPerSecond < current.EstimatedGenerationSpeed.MinTokensPerSecond {
				continue
			}
			if candidate.EstimatedGenerationSpeed.MaxTokensPerSecond > current.EstimatedGenerationSpeed.MaxTokensPerSecond {
				best = index
				continue
			}
			if candidate.EstimatedGenerationSpeed.MaxTokensPerSecond < current.EstimatedGenerationSpeed.MaxTokensPerSecond {
				continue
			}
		}
		if candidate.weightsBytes > 0 && (current.weightsBytes <= 0 || candidate.weightsBytes < current.weightsBytes) {
			best = index
		}
	}
	return best
}
