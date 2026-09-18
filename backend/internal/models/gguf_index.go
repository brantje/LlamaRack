package models

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/brantje/llamarack/backend/internal/ggufmeta"
)

type ggufIndexEntry struct {
	SizeBytes int64
	MTimeNS   int64
	Summary   ggufmeta.Summary
	Warning   string
}

func (s *Service) loadGGUFIndex(ctx context.Context) (map[string]ggufIndexEntry, error) {
	index, err := s.store.LoadGGUFIndex(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ggufIndexEntry, len(index))
	for path, entry := range index {
		out[filepath.ToSlash(filepath.Clean(path))] = entry
	}
	return out, nil
}

func (s *Service) cachedGGUFSummary(
	ctx context.Context,
	root, rel string,
	sizeBytes, mtimeNS int64,
	index map[string]ggufIndexEntry,
) (ggufmeta.Summary, string, error) {
	key := filepath.ToSlash(filepath.Clean(rel))
	if entry, ok := index[key]; ok && entry.SizeBytes == sizeBytes && entry.MTimeNS == mtimeNS {
		return entry.Summary, entry.Warning, nil
	}

	absolute := filepath.Join(root, filepath.FromSlash(key))
	summary, inspectErr := ggufmeta.ReadSummary(absolute)
	warning := ""
	if inspectErr != nil {
		warning = inspectErr.Error()
		summary = ggufmeta.Summary{}
	}
	entry := ggufIndexEntry{SizeBytes: sizeBytes, MTimeNS: mtimeNS, Summary: summary, Warning: warning}
	if err := s.storeGGUFIndex(ctx, key, entry); err != nil {
		return ggufmeta.Summary{}, "", err
	}
	index[key] = entry
	return summary, warning, nil
}

func (s *Service) storeGGUFIndex(ctx context.Context, path string, entry ggufIndexEntry) error {
	return s.store.StoreGGUFIndex(ctx, path, entry)
}

func (s *Service) removeMissingGGUFIndex(ctx context.Context, index map[string]ggufIndexEntry, seen map[string]bool) error {
	for path := range index {
		if seen[path] {
			continue
		}
		if err := s.store.DeleteGGUFIndex(ctx, path); err != nil {
			return err
		}
		delete(index, path)
	}
	return nil
}

// GGUFSummary validates a model path and returns cached metadata/classification
// when the file fingerprint is unchanged. New or modified files are inspected
// once and persisted for future discovery and metadata requests.
func (s *Service) GGUFSummary(ctx context.Context, path string) (ggufmeta.Summary, error) {
	rel, info, err := s.resolveGGUF(path)
	if err != nil {
		return ggufmeta.Summary{}, err
	}
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return ggufmeta.Summary{}, err
	}
	index, err := s.loadGGUFIndex(ctx)
	if err != nil {
		return ggufmeta.Summary{}, err
	}
	summary, warning, err := s.cachedGGUFSummary(ctx, root, rel, info.Size(), info.ModTime().UnixNano(), index)
	if err != nil {
		return ggufmeta.Summary{}, err
	}
	if warning != "" {
		return ggufmeta.Summary{}, errors.New(warning)
	}
	return summary, nil
}

func ggufIndexUint(value uint64) (int64, error) {
	if value > uint64(^uint64(0)>>1) {
		return 0, fmt.Errorf("GGUF index value %d exceeds SQLite INTEGER range", value)
	}
	return int64(value), nil
}
