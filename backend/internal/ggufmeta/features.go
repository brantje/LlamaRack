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

// FeaturesFromInspection derives metadata-only features. MTPOnly is populated
// by DetectFeatures because distinguishing a bundled model from an MTP-only
// helper requires looking at tensor names in the GGUF tensor directory.
func FeaturesFromInspection(inspection Inspection) Features {
	architecture := strings.TrimSpace(inspection.Derived.Architecture)
	features := Features{
		Architecture: architecture,
		Projector:    strings.EqualFold(architecture, "clip"),
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
// directory. Tensor payloads are never read. llama.cpp MTP-only exports keep
// NextN metadata but omit the normal trunk blk.0 tensors; bundled/native MTP
// models contain both trunk and NextN tensors.
func DetectFeatures(path string) (Features, error) {
	inspection, err := Inspect(path)
	if err != nil {
		return Features{}, err
	}
	features := FeaturesFromInspection(inspection)
	if !features.HasMTP {
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
