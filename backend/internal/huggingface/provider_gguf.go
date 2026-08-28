package huggingface

import "math"

// GGUFInfo mirrors the provider-owned GGUF summary returned by the Hugging Face
// model-info API. The Hub computes these values from the model's GGUF data, so
// Discover should prefer them over filename heuristics whenever they are
// available.
type GGUFInfo struct {
	Total         int64            `json:"total,omitempty"`
	Parameters    map[string]int64 `json:"parameters,omitempty"`
	Architecture  string           `json:"architecture,omitempty"`
	ContextLength int64            `json:"context_length,omitempty"`
}

func ggufParameterCount(info *GGUFInfo) int64 {
	if info == nil {
		return 0
	}
	var total int64
	for _, count := range info.Parameters {
		if count > 0 {
			total += count
		}
	}
	if total > 0 {
		return total
	}
	if info.Total > 0 {
		return info.Total
	}
	return 0
}

func artifactBitsPerWeight(modelBytes, parameterCount int64) float64 {
	if modelBytes <= 0 || parameterCount <= 0 {
		return 0
	}
	value := float64(modelBytes) * 8 / float64(parameterCount)
	if value <= 0 || value > 64 {
		return 0
	}
	return math.Round(value*100) / 100
}
