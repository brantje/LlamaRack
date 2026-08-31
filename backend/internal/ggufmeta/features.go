package ggufmeta

import (
	"strconv"
	"strings"
)

const maxTensorDimensions = uint32(8)

// Features contains capability/classification facts that can be derived from a
// GGUF header without loading any tensor payloads.
type Features struct {
	Architecture       string `json:"architecture,omitempty"`
	NextNPredictLayers int64  `json:"nextn_predict_layers,omitempty"`
	HasMTP             bool   `json:"has_mtp"`
	MTPOnly            bool   `json:"mtp_only"`
	Projector          bool   `json:"projector"`
}

// IsStandaloneMTPArchitecture identifies GGUF architectures whose model
// contract is itself a speculative draft/helper model. These architectures may
// contain normal blk.0.* tensors, so tensor-prefix heuristics cannot distinguish
// them from selectable target models.
func IsStandaloneMTPArchitecture(architecture string) bool {
	switch strings.ToLower(strings.TrimSpace(architecture)) {
	case "gemma4-assistant", "gemma4_assistant":
		return true
	default:
		return false
	}
}

// FeaturesFromInspection derives metadata-only features. MTPOnly is populated
// directly for architectures that are standalone draft models; other MTP
// formats are refined by Inspect using tensor names.
func FeaturesFromInspection(inspection Inspection) Features {
	architecture := strings.TrimSpace(inspection.Derived.Architecture)
	standaloneMTP := IsStandaloneMTPArchitecture(architecture)
	features := Features{
		Architecture: architecture,
		Projector:    strings.EqualFold(architecture, "clip"),
		HasMTP:       standaloneMTP,
		MTPOnly:      standaloneMTP,
	}
	if architecture == "" {
		return features
	}
	key := architecture + ".nextn_predict_layers"
	for _, entry := range inspection.Metadata {
		if entry.Key != key {
			continue
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(entry.Value), 10, 64); err == nil && n > 0 {
			features.NextNPredictLayers = n
			features.HasMTP = true
			return features
		}
		if n, err := strconv.ParseUint(strings.TrimSpace(entry.Value), 10, 64); err == nil && n > 0 && n <= uint64(^uint64(0)>>1) {
			features.NextNPredictLayers = int64(n)
			features.HasMTP = true
			return features
		}
		return features
	}
	return features
}

// DetectFeatures inspects GGUF metadata and, for MTP-capable files, the tensor
// directory. Tensor payloads are never read. Architecture-defined standalone
// draft models are already MTP-only even when they contain blk.0.* tensors.
// Other MTP formats are considered helper-only when they keep NextN metadata
// but omit the target model's normal trunk tensors.
func DetectFeatures(path string) (Features, error) {
	inspection, err := Inspect(path)
	if err != nil {
		return Features{}, err
	}
	return inspection.Features, nil
}
