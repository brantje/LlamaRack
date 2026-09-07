package ggufmeta

import "io"

type metadataBudget struct {
	retained uint64
	consumed uint64
}

func (b *metadataBudget) addRetained(n uint64) error {
	if n > maxRetainedMetadataBytes || b.retained > maxRetainedMetadataBytes-n {
		return errMetadataBudgetExceeded
	}
	b.retained += n
	return nil
}

func (b *metadataBudget) addConsumed(n uint64) error {
	if n > maxMetadataSectionBytes || b.consumed > maxMetadataSectionBytes-n {
		return errMetadataBudgetExceeded
	}
	b.consumed += n
	return nil
}

func (b *metadataBudget) retain(parts ...string) error {
	var n uint64
	for _, part := range parts {
		n += uint64(len(part))
	}
	return b.addRetained(n)
}

type countingReader struct {
	r      io.Reader
	budget *metadataBudget
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		if addErr := c.budget.addConsumed(uint64(n)); addErr != nil {
			return n, addErr
		}
	}
	return n, err
}

func boundMetadataReader(r io.Reader) (io.Reader, *metadataBudget) {
	budget := &metadataBudget{}
	return &countingReader{r: r, budget: budget}, budget
}

func checkMetadataCount(metadataCount uint64) error {
	if metadataCount > maxMetadataCount {
		return errUnreasonableMetadataCount
	}
	return nil
}
