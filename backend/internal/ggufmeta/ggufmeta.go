package ggufmeta

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	maxMetadataCount       = uint64(1_000_000)
	maxArrayCount          = uint64(10_000_000)
	maxStringBytes         = uint64(16 * 1024 * 1024)
	maxDisplayBytes        = uint64(4096)
	maxKeyBytes            = uint64(64 * 1024)
	maxArrayPreview        = uint64(16)
	metadataReadBufferSize = 8 * 1024
)

type Entry struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Value       string `json:"value"`
	Truncated   bool   `json:"truncated,omitempty"`
	ArrayLength uint64 `json:"array_length,omitempty"`
}

type Derived struct {
	Architecture  string `json:"architecture,omitempty"`
	ContextLength int64  `json:"context_length,omitempty"`
	BlockCount    int64  `json:"block_count,omitempty"`
	Embedding     int64  `json:"embedding_length,omitempty"`
	HeadCount     int64  `json:"head_count,omitempty"`
	KVHeadCount   int64  `json:"kv_head_count,omitempty"`
	KeyLength     int64  `json:"key_length,omitempty"`
	ValueLength   int64  `json:"value_length,omitempty"`
}

type Inspection struct {
	Version       uint32   `json:"version"`
	TensorCount   uint64   `json:"tensor_count"`
	MetadataCount uint64   `json:"metadata_count"`
	Metadata      []Entry  `json:"metadata"`
	Derived       Derived  `json:"derived"`
	Warnings      []string `json:"warnings,omitempty"`
}

func Inspect(path string) (Inspection, error) {
	f, err := os.Open(path)
	if err != nil {
		return Inspection{}, err
	}
	defer f.Close()
	return inspect(bufio.NewReaderSize(f, metadataReadBufferSize))
}

func inspect(r io.Reader) (Inspection, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return Inspection{}, err
	}
	if string(magic[:]) != "GGUF" {
		return Inspection{}, errors.New("GGUF metadata unavailable: invalid magic")
	}
	version, err := readU32(r)
	if err != nil {
		return Inspection{}, err
	}
	if version < 2 || version > 3 {
		return Inspection{}, fmt.Errorf("GGUF metadata unavailable: unsupported version %d", version)
	}
	tensorCount, err := readU64(r)
	if err != nil {
		return Inspection{}, err
	}
	metadataCount, err := readU64(r)
	if err != nil {
		return Inspection{}, err
	}
	if metadataCount > maxMetadataCount {
		return Inspection{}, errors.New("GGUF metadata unavailable: unreasonable metadata count")
	}

	result := Inspection{Version: version, TensorCount: tensorCount, MetadataCount: metadataCount, Metadata: make([]Entry, 0, metadataCount)}
	scalars := make(map[string]string)
	for i := uint64(0); i < metadataCount; i++ {
		key, err := readKey(r)
		if err != nil {
			return Inspection{}, err
		}
		typeID, err := readU32(r)
		if err != nil {
			return Inspection{}, err
		}
		value, err := readValue(r, typeID)
		if err != nil {
			return Inspection{}, fmt.Errorf("GGUF metadata %q: %w", key, err)
		}
		entry := Entry{Key: key, Type: value.typeName, Value: value.display, Truncated: value.truncated, ArrayLength: value.arrayLength}
		result.Metadata = append(result.Metadata, entry)
		if value.scalar {
			scalars[key] = value.display
		}
	}
	sort.Slice(result.Metadata, func(i, j int) bool { return result.Metadata[i].Key < result.Metadata[j].Key })
	result.Derived = derive(scalars)
	return result, nil
}

func Filter(entries []Entry, query string, offset, limit int) ([]Entry, int) {
	query = strings.ToLower(strings.TrimSpace(query))
	filtered := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if query == "" || strings.Contains(strings.ToLower(entry.Key), query) {
			filtered = append(filtered, entry)
		}
	}
	total := len(filtered)
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset >= len(filtered) {
		return []Entry{}, total
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[offset:end], total
}

type valueResult struct {
	typeName    string
	display     string
	truncated   bool
	arrayLength uint64
	scalar      bool
}

func readValue(r io.Reader, typeID uint32) (valueResult, error) {
	switch typeID {
	case 0:
		v, err := readUint(r, 1)
		return scalarResult("uint8", v, err)
	case 1:
		v, err := readInt(r, 1)
		return scalarResult("int8", v, err)
	case 2:
		v, err := readUint(r, 2)
		return scalarResult("uint16", v, err)
	case 3:
		v, err := readInt(r, 2)
		return scalarResult("int16", v, err)
	case 4:
		v, err := readUint(r, 4)
		return scalarResult("uint32", v, err)
	case 5:
		v, err := readInt(r, 4)
		return scalarResult("int32", v, err)
	case 6:
		bits, err := readUint(r, 4)
		if err != nil {
			return valueResult{}, err
		}
		return valueResult{typeName: "float32", display: strconv.FormatFloat(float64(math.Float32frombits(uint32(bits))), 'g', -1, 32), scalar: true}, nil
	case 7:
		v, err := readUint(r, 1)
		if err != nil {
			return valueResult{}, err
		}
		return valueResult{typeName: "bool", display: strconv.FormatBool(v != 0), scalar: true}, nil
	case 8:
		value, truncated, err := readString(r)
		if err != nil {
			return valueResult{}, err
		}
		return valueResult{typeName: "string", display: value, truncated: truncated, scalar: true}, nil
	case 9:
		return readArray(r)
	case 10:
		v, err := readUint(r, 8)
		return scalarResult("uint64", v, err)
	case 11:
		v, err := readInt(r, 8)
		return scalarResult("int64", v, err)
	case 12:
		bits, err := readUint(r, 8)
		if err != nil {
			return valueResult{}, err
		}
		return valueResult{typeName: "float64", display: strconv.FormatFloat(math.Float64frombits(bits), 'g', -1, 64), scalar: true}, nil
	default:
		return valueResult{}, fmt.Errorf("unsupported value type %d", typeID)
	}
}

func readArray(r io.Reader) (valueResult, error) {
	elemType, err := readU32(r)
	if err != nil {
		return valueResult{}, err
	}
	count, err := readU64(r)
	if err != nil {
		return valueResult{}, err
	}
	if count > maxArrayCount {
		return valueResult{}, errors.New("GGUF metadata array is unreasonable")
	}
	if elemType == 9 {
		return valueResult{}, errors.New("nested GGUF metadata arrays are unsupported")
	}
	typeName, ok := typeName(elemType)
	if !ok {
		return valueResult{}, fmt.Errorf("unsupported array element type %d", elemType)
	}

	previewCount := count
	if previewCount > maxArrayPreview {
		previewCount = maxArrayPreview
	}
	preview := make([]string, 0, previewCount)
	truncated := count > previewCount
	for i := uint64(0); i < previewCount; i++ {
		value, err := readValue(r, elemType)
		if err != nil {
			return valueResult{}, err
		}
		text := value.display
		if elemType == 8 {
			text = strconv.Quote(text)
		}
		preview = append(preview, text)
		truncated = truncated || value.truncated
	}
	if remaining := count - previewCount; remaining > 0 {
		if err := skipArrayRemainder(r, elemType, remaining); err != nil {
			return valueResult{}, err
		}
	}
	display := "[" + strings.Join(preview, ", ")
	if count > previewCount {
		if len(preview) > 0 {
			display += ", "
		}
		display += fmt.Sprintf("… %d more", count-previewCount)
	}
	display += "]"
	return valueResult{typeName: "array<" + typeName + ">", display: display, truncated: truncated, arrayLength: count}, nil
}

func skipArrayRemainder(r io.Reader, elemType uint32, remaining uint64) error {
	if size, ok := fixedSize(elemType); ok {
		if remaining > uint64(^uint64(0)>>1)/uint64(size) {
			return errors.New("GGUF metadata array is too large to seek")
		}
		return skipBytes(r, int64(remaining)*size)
	}
	for i := uint64(0); i < remaining; i++ {
		if err := skipValue(r, elemType); err != nil {
			return err
		}
	}
	return nil
}

func skipValue(r io.Reader, typeID uint32) error {
	if size, ok := fixedSize(typeID); ok {
		return skipBytes(r, size)
	}
	if typeID == 8 {
		n, err := readU64(r)
		if err != nil {
			return err
		}
		if n > maxStringBytes {
			return errors.New("GGUF metadata string is unreasonable")
		}
		return skipBytes(r, int64(n))
	}
	return fmt.Errorf("unsupported array element type %d", typeID)
}

type byteDiscarder interface {
	Discard(int) (int, error)
}

func skipBytes(r io.Reader, n int64) error {
	if n <= 0 {
		return nil
	}
	if d, ok := r.(byteDiscarder); ok {
		for n > 0 {
			chunk := n
			if chunk > int64(math.MaxInt) {
				chunk = int64(math.MaxInt)
			}
			discarded, err := d.Discard(int(chunk))
			n -= int64(discarded)
			if err != nil {
				return err
			}
			if discarded == 0 {
				return io.EOF
			}
		}
		return nil
	}
	if rs, ok := r.(io.Seeker); ok {
		_, err := rs.Seek(n, io.SeekCurrent)
		return err
	}
	_, err := io.CopyN(io.Discard, r, n)
	return err
}

func fixedSize(typeID uint32) (int64, bool) {
	switch typeID {
	case 0, 1, 7:
		return 1, true
	case 2, 3:
		return 2, true
	case 4, 5, 6:
		return 4, true
	case 10, 11, 12:
		return 8, true
	default:
		return 0, false
	}
}

func typeName(typeID uint32) (string, bool) {
	names := map[uint32]string{0: "uint8", 1: "int8", 2: "uint16", 3: "int16", 4: "uint32", 5: "int32", 6: "float32", 7: "bool", 8: "string", 10: "uint64", 11: "int64", 12: "float64"}
	name, ok := names[typeID]
	return name, ok
}

func scalarResult[T ~uint64 | ~int64](name string, value T, err error) (valueResult, error) {
	if err != nil {
		return valueResult{}, err
	}
	return valueResult{typeName: name, display: fmt.Sprint(value), scalar: true}, nil
}

func readUint(r io.Reader, size int) (uint64, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:size]); err != nil {
		return 0, err
	}
	switch size {
	case 1:
		return uint64(buf[0]), nil
	case 2:
		return uint64(binary.LittleEndian.Uint16(buf[:2])), nil
	case 4:
		return uint64(binary.LittleEndian.Uint32(buf[:4])), nil
	case 8:
		return binary.LittleEndian.Uint64(buf[:]), nil
	default:
		return 0, errors.New("invalid integer width")
	}
}

func readInt(r io.Reader, size int) (int64, error) {
	u, err := readUint(r, size)
	if err != nil {
		return 0, err
	}
	switch size {
	case 1:
		return int64(int8(u)), nil
	case 2:
		return int64(int16(u)), nil
	case 4:
		return int64(int32(u)), nil
	case 8:
		return int64(u), nil
	default:
		return 0, errors.New("invalid integer width")
	}
}

func readKey(r io.Reader) (string, error) {
	n, err := readU64(r)
	if err != nil {
		return "", err
	}
	if n > maxKeyBytes {
		return "", errors.New("GGUF metadata key is unreasonable")
	}
	buf := make([]byte, int(n))
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readString(r io.Reader) (string, bool, error) {
	n, err := readU64(r)
	if err != nil {
		return "", false, err
	}
	if n > maxStringBytes {
		return "", false, errors.New("GGUF metadata string is unreasonable")
	}
	take := n
	truncated := false
	if take > maxDisplayBytes {
		take = maxDisplayBytes
		truncated = true
	}
	buf := make([]byte, int(take))
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", false, err
	}
	if n > take {
		if err := skipBytes(r, int64(n-take)); err != nil {
			return "", false, err
		}
	}
	value := string(buf)
	if truncated {
		value += "…"
	}
	return value, truncated, nil
}

func readU32(r io.Reader) (uint32, error) {
	if br, ok := r.(*bufio.Reader); ok {
		b, err := br.Peek(4)
		if err != nil {
			return 0, err
		}
		v := binary.LittleEndian.Uint32(b)
		_, _ = br.Discard(4)
		return v, nil
	}
	v, err := readUint(r, 4)
	return uint32(v), err
}

func readU64(r io.Reader) (uint64, error) {
	if br, ok := r.(*bufio.Reader); ok {
		b, err := br.Peek(8)
		if err != nil {
			return 0, err
		}
		v := binary.LittleEndian.Uint64(b)
		_, _ = br.Discard(8)
		return v, nil
	}
	return readUint(r, 8)
}

func derive(values map[string]string) Derived {
	architecture := strings.TrimSpace(values["general.architecture"])
	d := Derived{Architecture: architecture}
	prefix := architecture + "."
	d.ContextLength = exactInt(values, prefix+"context_length")
	d.BlockCount = exactInt(values, prefix+"block_count")
	d.Embedding = exactInt(values, prefix+"embedding_length")
	d.HeadCount = exactInt(values, prefix+"attention.head_count")
	d.KVHeadCount = exactInt(values, prefix+"attention.head_count_kv")
	d.KeyLength = exactInt(values, prefix+"attention.key_length")
	d.ValueLength = exactInt(values, prefix+"attention.value_length")
	return d
}

func exactInt(values map[string]string, key string) int64 {
	if key == "." {
		return 0
	}
	raw, ok := values[key]
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err == nil {
		return n
	}
	u, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || u > math.MaxInt64 {
		return 0
	}
	return int64(u)
}
