package ggufmeta

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type seekFailureReader struct {
	*bytes.Reader
}

func (r seekFailureReader) Seek(offset int64, whence int) (int64, error) {
	return 0, errors.New("seek failed")
}

func TestSummarySkipPropagatesSeekFailures(t *testing.T) {
	if err := skipSummaryValue(seekFailureReader{bytes.NewReader(nil)}, 4); err == nil || !strings.Contains(err.Error(), "seek failed") {
		t.Fatalf("fixed seek err=%v", err)
	}

	var text bytes.Buffer
	write(&text, uint64(4))
	if err := skipSummaryString(seekFailureReader{bytes.NewReader(text.Bytes())}); err == nil || !strings.Contains(err.Error(), "seek failed") {
		t.Fatalf("string seek err=%v", err)
	}

	var fixedArray bytes.Buffer
	write(&fixedArray, uint32(4))
	write(&fixedArray, uint64(2))
	if err := skipSummaryValue(seekFailureReader{bytes.NewReader(fixedArray.Bytes())}, 9); err == nil || !strings.Contains(err.Error(), "seek failed") {
		t.Fatalf("array seek err=%v", err)
	}
}

func TestReadSummaryHeaderAndMetadataReadFailures(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "partial magic", data: []byte("GG")},
		{name: "missing version", data: []byte("GGUF")},
		{name: "missing tensor count", data: appendHeader([]byte("GGUF"), uint32(3))},
		{name: "missing metadata count", data: appendHeader(appendHeader([]byte("GGUF"), uint32(3)), uint64(0))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := readSummary(bytes.NewReader(tc.data)); err == nil {
				t.Fatal("expected read failure")
			}
		})
	}

	var missingKey bytes.Buffer
	missingKey.WriteString("GGUF")
	write(&missingKey, uint32(3))
	write(&missingKey, uint64(0))
	write(&missingKey, uint64(1))
	if _, err := readSummary(bytes.NewReader(missingKey.Bytes())); err == nil {
		t.Fatal("missing metadata key should fail")
	}

	var missingType bytes.Buffer
	missingType.WriteString("GGUF")
	write(&missingType, uint32(3))
	write(&missingType, uint64(0))
	write(&missingType, uint64(1))
	writeString(&missingType, "ignored")
	if _, err := readSummary(bytes.NewReader(missingType.Bytes())); err == nil {
		t.Fatal("missing metadata type should fail")
	}
}

func appendHeader[T uint32 | uint64](dst []byte, value T) []byte {
	var b bytes.Buffer
	_, _ = b.Write(dst)
	write(&b, value)
	return b.Bytes()
}

func TestTensorPrefixPresentCurrentReadFailures(t *testing.T) {
	var missingType bytes.Buffer
	writeString(&missingType, "blk.0.weight")
	write(&missingType, uint32(1))
	write(&missingType, uint64(1))
	if _, err := tensorPrefixPresentCurrent(bytes.NewReader(missingType.Bytes()), 1, "blk.0."); err == nil {
		t.Fatal("missing tensor type should fail")
	}

	var missingOffset bytes.Buffer
	writeString(&missingOffset, "blk.0.weight")
	write(&missingOffset, uint32(0))
	write(&missingOffset, uint32(0))
	if _, err := tensorPrefixPresentCurrent(bytes.NewReader(missingOffset.Bytes()), 1, "blk.0."); err == nil {
		t.Fatal("missing tensor offset should fail")
	}

	var valid bytes.Buffer
	writeString(&valid, "blk.0.weight")
	write(&valid, uint32(0))
	write(&valid, uint32(0))
	write(&valid, uint64(0))
	found, err := tensorPrefixPresentCurrent(bytes.NewReader(valid.Bytes()), 1, "blk.0.")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

var _ io.ReadSeeker = seekFailureReader{}
