package ggufmeta

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Summary contains the small, stable subset of GGUF information needed by
// discovery, automatic defaults and hardware recommendations. Unlike Inspect,
// it does not materialize or sort the complete metadata table.
type Summary struct {
	Version       uint32
	TensorCount   uint64
	MetadataCount uint64
	Derived       Derived
	Features      Features
}

// ReadSummary reads GGUF metadata once and, only for MTP-capable files whose
// architecture does not already define them as standalone helpers, continues
// directly into the tensor directory to distinguish native/bundled MTP from an
// MTP-only helper. Tensor payloads are never read.
func ReadSummary(path string) (Summary, error) {
	f, err := os.Open(path)
	if err != nil {
		return Summary{}, err
	}
	defer f.Close()
	return readSummary(bufio.NewReaderSize(f, metadataReadBufferSize))
}

func readSummary(r io.Reader) (Summary, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return Summary{}, err
	}
	if string(magic[:]) != "GGUF" {
		return Summary{}, errors.New("GGUF metadata unavailable: invalid magic")
	}
	version, err := readU32(r)
	if err != nil {
		return Summary{}, err
	}
	if version < 2 || version > 3 {
		return Summary{}, fmt.Errorf("GGUF metadata unavailable: unsupported version %d", version)
	}
	tensorCount, err := readU64(r)
	if err != nil {
		return Summary{}, err
	}
	metadataCount, err := readU64(r)
	if err != nil {
		return Summary{}, err
	}
	if metadataCount > maxMetadataCount {
		return Summary{}, errors.New("GGUF metadata unavailable: unreasonable metadata count")
	}

	values := make(map[string]string, 12)
	for i := uint64(0); i < metadataCount; i++ {
		key, err := readKey(r)
		if err != nil {
			return Summary{}, err
		}
		typeID, err := readU32(r)
		if err != nil {
			return Summary{}, err
		}
		if summaryMetadataKey(key) {
			value, err := readValue(r, typeID)
			if err != nil {
				return Summary{}, fmt.Errorf("GGUF metadata %q: %w", key, err)
			}
			if value.scalar {
				values[key] = value.display
			}
			continue
		}
		if err := skipSummaryValue(r, typeID); err != nil {
			return Summary{}, fmt.Errorf("GGUF metadata %q: %w", key, err)
		}
	}

	derived := derive(values)
	features := summaryFeatures(derived, values)
	if features.HasMTP && !features.MTPOnly {
		hasTrunk, err := tensorPrefixPresentCurrent(r, tensorCount, "blk.0.")
		if err != nil {
			return Summary{}, err
		}
		features.MTPOnly = !hasTrunk
	}
	return Summary{
		Version:       version,
		TensorCount:   tensorCount,
		MetadataCount: metadataCount,
		Derived:       derived,
		Features:      features,
	}, nil
}

func summaryMetadataKey(key string) bool {
	if key == "general.architecture" {
		return true
	}
	for _, suffix := range []string{
		".context_length",
		".block_count",
		".embedding_length",
		".attention.head_count",
		".attention.head_count_kv",
		".attention.key_length",
		".attention.value_length",
		".nextn_predict_layers",
	} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func summaryFeatures(derived Derived, values map[string]string) Features {
	architecture := strings.TrimSpace(derived.Architecture)
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
	raw := strings.TrimSpace(values[architecture+".nextn_predict_layers"])
	if raw == "" {
		return features
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
		features.NextNPredictLayers = n
		features.HasMTP = true
		return features
	}
	if n, err := strconv.ParseUint(raw, 10, 64); err == nil && n > 0 && n <= uint64(^uint64(0)>>1) {
		features.NextNPredictLayers = int64(n)
		features.HasMTP = true
	}
	return features
}

func skipSummaryValue(r io.Reader, typeID uint32) error {
	if size, ok := fixedSize(typeID); ok {
		return skipBytes(r, size)
	}
	if typeID == 8 {
		return skipSummaryString(r)
	}
	if typeID != 9 {
		return fmt.Errorf("unsupported value type %d", typeID)
	}

	elemType, err := readU32(r)
	if err != nil {
		return err
	}
	count, err := readU64(r)
	if err != nil {
		return err
	}
	if count > maxArrayCount {
		return errors.New("GGUF metadata array is unreasonable")
	}
	if elemType == 9 {
		return errors.New("nested GGUF metadata arrays are unsupported")
	}
	if size, ok := fixedSize(elemType); ok {
		if count > uint64(^uint64(0)>>1)/uint64(size) {
			return errors.New("GGUF metadata array is too large to seek")
		}
		return skipBytes(r, int64(count*uint64(size)))
	}
	if elemType == 8 {
		for i := uint64(0); i < count; i++ {
			if err := skipSummaryString(r); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unsupported array element type %d", elemType)
}

func skipSummaryString(r io.Reader) error {
	n, err := readU64(r)
	if err != nil {
		return err
	}
	if n > maxStringBytes {
		return errors.New("GGUF metadata string is unreasonable")
	}
	if n > uint64(^uint64(0)>>1) {
		return errors.New("GGUF metadata string is too large to seek")
	}
	return skipBytes(r, int64(n))
}

func tensorPrefixPresentCurrent(r io.Reader, tensorCount uint64, prefix string) (bool, error) {
	for i := uint64(0); i < tensorCount; i++ {
		name, _, err := readString(r)
		if err != nil {
			return false, err
		}
		dimensions, err := readU32(r)
		if err != nil {
			return false, err
		}
		if dimensions > maxTensorDimensions {
			return false, errors.New("GGUF tensor has unreasonable dimension count")
		}
		for dimension := uint32(0); dimension < dimensions; dimension++ {
			if _, err := readU64(r); err != nil {
				return false, err
			}
		}
		if _, err := readU32(r); err != nil {
			return false, err
		}
		if _, err := readU64(r); err != nil {
			return false, err
		}
		if strings.HasPrefix(name, prefix) {
			return true, nil
		}
	}
	return false, nil
}
