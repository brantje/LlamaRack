package ggufmeta

import (
	"errors"
	"fmt"
	"io"
	"os"
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
// formats are refined by DetectFeatures using tensor names.
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
	features := FeaturesFromInspection(inspection)
	if !features.HasMTP || features.MTPOnly {
		return features, nil
	}
	hasTrunk, err := tensorPrefixPresent(path, "blk.0.")
	if err != nil {
		return Features{}, err
	}
	features.MTPOnly = !hasTrunk
	return features, nil
}

func tensorPrefixPresent(path, prefix string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false, err
	}
	if string(magic[:]) != "GGUF" {
		return false, errors.New("GGUF metadata unavailable: invalid magic")
	}
	version, err := readU32(f)
	if err != nil {
		return false, err
	}
	if version < 2 || version > 3 {
		return false, fmt.Errorf("GGUF metadata unavailable: unsupported version %d", version)
	}
	tensorCount, err := readU64(f)
	if err != nil {
		return false, err
	}
	metadataCount, err := readU64(f)
	if err != nil {
		return false, err
	}
	if metadataCount > maxMetadataCount {
		return false, errors.New("GGUF metadata unavailable: unreasonable metadata count")
	}

	for i := uint64(0); i < metadataCount; i++ {
		if _, err := readKey(f); err != nil {
			return false, err
		}
		typeID, err := readU32(f)
		if err != nil {
			return false, err
		}
		if _, err := readValue(f, typeID); err != nil {
			return false, err
		}
	}

	for i := uint64(0); i < tensorCount; i++ {
		name, _, err := readString(f)
		if err != nil {
			return false, err
		}
		dimensions, err := readU32(f)
		if err != nil {
			return false, err
		}
		if dimensions > maxTensorDimensions {
			return false, errors.New("GGUF tensor has unreasonable dimension count")
		}
		for dimension := uint32(0); dimension < dimensions; dimension++ {
			if _, err := readU64(f); err != nil {
				return false, err
			}
		}
		if _, err := readU32(f); err != nil { // ggml tensor type
			return false, err
		}
		if _, err := readU64(f); err != nil { // tensor data offset
			return false, err
		}
		if strings.HasPrefix(name, prefix) {
			return true, nil
		}
	}
	return false, nil
}
