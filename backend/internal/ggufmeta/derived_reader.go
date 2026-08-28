package ggufmeta

import (
	"errors"
	"io"
)

// InspectDerivedReader reads the GGUF metadata section needed by the hardware
// recommendation engine. Metadata keys are not ordered by category, so the
// parser must continue through tokenizer entries instead of assuming all model
// architecture fields appear first. The caller is responsible for bounding the
// reader; this function never advances into tensor descriptors or tensor data.
func InspectDerivedReader(reader io.Reader) (Derived, error) {
	if reader == nil {
		return Derived{}, errors.New("GGUF metadata unavailable: empty reader")
	}
	r := &forwardReadSeeker{reader: reader}
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return Derived{}, err
	}
	if string(magic[:]) != "GGUF" {
		return Derived{}, errors.New("GGUF metadata unavailable: invalid magic")
	}
	version, err := readU32(r)
	if err != nil {
		return Derived{}, err
	}
	if version < 2 || version > 3 {
		return Derived{}, errors.New("GGUF metadata unavailable: unsupported version")
	}
	if _, err := readU64(r); err != nil { // tensor count
		return Derived{}, err
	}
	metadataCount, err := readU64(r)
	if err != nil {
		return Derived{}, err
	}
	if metadataCount > maxMetadataCount {
		return Derived{}, errors.New("GGUF metadata unavailable: unreasonable metadata count")
	}

	scalars := make(map[string]string)
	for i := uint64(0); i < metadataCount; i++ {
		key, err := readKey(r)
		if err != nil {
			return derive(scalars), err
		}
		typeID, err := readU32(r)
		if err != nil {
			return derive(scalars), err
		}
		value, err := readValue(r, typeID)
		if err != nil {
			return derive(scalars), err
		}
		if value.scalar {
			scalars[key] = value.display
		}
	}
	derived := derive(scalars)
	if !derivedCoreReady(derived) {
		return derived, errors.New("GGUF metadata unavailable: architecture dimensions are incomplete")
	}
	return derived, nil
}

func derivedCoreReady(value Derived) bool {
	return value.Architecture != "" && value.BlockCount > 0 && value.Embedding > 0 && value.HeadCount > 0
}

type forwardReadSeeker struct {
	reader io.Reader
	pos    int64
}

func (r *forwardReadSeeker) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.pos += int64(n)
	return n, err
}

func (r *forwardReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if whence != io.SeekCurrent || offset < 0 {
		return r.pos, errors.New("GGUF remote metadata reader only supports forward seeks")
	}
	if offset == 0 {
		return r.pos, nil
	}
	n, err := io.CopyN(io.Discard, r.reader, offset)
	r.pos += n
	if err != nil {
		return r.pos, err
	}
	return r.pos, nil
}
