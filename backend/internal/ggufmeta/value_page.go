package ggufmeta

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	maxValuePageArrayItems = uint64(100)
	maxValuePageStringBytes = uint64(16 * 1024)
)

var ErrMetadataKeyNotFound = errors.New("GGUF metadata key not found")

type ValuePage struct {
	Key     string   `json:"key"`
	Type    string   `json:"type"`
	Value   string   `json:"value,omitempty"`
	Items   []string `json:"items,omitempty"`
	Offset  uint64   `json:"offset"`
	Limit   uint64   `json:"limit"`
	Total   uint64   `json:"total"`
	HasMore bool     `json:"has_more"`
}

// ReadValuePage lazily reads one GGUF metadata value. Strings are paged by byte
// range and arrays by element range. The rest of the GGUF metadata is scanned
// only far enough to locate the requested key; tensor descriptors/payloads are
// never read.
func ReadValuePage(path, key string, offset, limit uint64) (ValuePage, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return ValuePage{}, errors.New("metadata key is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return ValuePage{}, err
	}
	defer f.Close()

	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return ValuePage{}, err
	}
	if string(magic[:]) != "GGUF" {
		return ValuePage{}, errors.New("GGUF metadata unavailable: invalid magic")
	}
	version, err := readU32(f)
	if err != nil {
		return ValuePage{}, err
	}
	if version < 2 || version > 3 {
		return ValuePage{}, fmt.Errorf("GGUF metadata unavailable: unsupported version %d", version)
	}
	if _, err := readU64(f); err != nil {
		return ValuePage{}, err
	}
	metadataCount, err := readU64(f)
	if err != nil {
		return ValuePage{}, err
	}
	if metadataCount > maxMetadataCount {
		return ValuePage{}, errors.New("GGUF metadata unavailable: unreasonable metadata count")
	}

	for index := uint64(0); index < metadataCount; index++ {
		candidate, err := readKey(f)
		if err != nil {
			return ValuePage{}, err
		}
		typeID, err := readU32(f)
		if err != nil {
			return ValuePage{}, err
		}
		if candidate == key {
			page, err := readValuePage(f, typeID, offset, limit)
			if err != nil {
				return ValuePage{}, fmt.Errorf("GGUF metadata %q: %w", key, err)
			}
			page.Key = key
			return page, nil
		}
		if _, err := readValue(f, typeID); err != nil {
			return ValuePage{}, fmt.Errorf("GGUF metadata %q: %w", candidate, err)
		}
	}
	return ValuePage{}, ErrMetadataKeyNotFound
}

func readValuePage(r io.ReadSeeker, typeID uint32, offset, limit uint64) (ValuePage, error) {
	switch typeID {
	case 8:
		return readStringPage(r, offset, limit)
	case 9:
		return readArrayPage(r, offset, limit)
	default:
		value, err := readValue(r, typeID)
		if err != nil {
			return ValuePage{}, err
		}
		return ValuePage{Type: value.typeName, Value: value.display, Offset: 0, Limit: 1, Total: 1}, nil
	}
}

func readStringPage(r io.ReadSeeker, offset, limit uint64) (ValuePage, error) {
	total, err := readU64(r)
	if err != nil {
		return ValuePage{}, err
	}
	if total > maxStringBytes {
		return ValuePage{}, errors.New("GGUF metadata string is unreasonable")
	}
	if offset > total {
		offset = total
	}
	if limit == 0 || limit > maxValuePageStringBytes {
		limit = maxValuePageStringBytes
	}
	if _, err := r.Seek(int64(offset), io.SeekCurrent); err != nil {
		return ValuePage{}, err
	}
	take := limit
	if remaining := total - offset; take > remaining {
		take = remaining
	}
	buf := make([]byte, int(take))
	if _, err := io.ReadFull(r, buf); err != nil {
		return ValuePage{}, err
	}
	return ValuePage{
		Type: "string", Value: string(buf), Offset: offset, Limit: limit, Total: total,
		HasMore: offset+take < total,
	}, nil
}

func readArrayPage(r io.ReadSeeker, offset, limit uint64) (ValuePage, error) {
	elemType, err := readU32(r)
	if err != nil {
		return ValuePage{}, err
	}
	count, err := readU64(r)
	if err != nil {
		return ValuePage{}, err
	}
	if count > maxArrayCount {
		return ValuePage{}, errors.New("GGUF metadata array is unreasonable")
	}
	if elemType == 9 {
		return ValuePage{}, errors.New("nested GGUF metadata arrays are unsupported")
	}
	elemName, ok := typeName(elemType)
	if !ok {
		return ValuePage{}, fmt.Errorf("unsupported array element type %d", elemType)
	}
	if offset > count {
		offset = count
	}
	if limit == 0 || limit > maxValuePageArrayItems {
		limit = maxValuePageArrayItems
	}
	for index := uint64(0); index < offset; index++ {
		if err := skipValue(r, elemType); err != nil {
			return ValuePage{}, err
		}
	}
	pageCount := limit
	if remaining := count - offset; pageCount > remaining {
		pageCount = remaining
	}
	items := make([]string, 0, pageCount)
	for index := uint64(0); index < pageCount; index++ {
		value, err := readValue(r, elemType)
		if err != nil {
			return ValuePage{}, err
		}
		text := value.display
		if elemType == 8 {
			text = strconv.Quote(text)
		}
		items = append(items, text)
	}
	return ValuePage{
		Type: "array<" + elemName + ">", Items: items, Offset: offset, Limit: limit, Total: count,
		HasMore: offset+uint64(len(items)) < count,
	}, nil
}
