package models

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
)

type ggufIndexEntry struct {
	SizeBytes int64
	MTimeNS   int64
	Summary   ggufmeta.Summary
	Warning   string
}

func (s *Service) loadGGUFIndex(ctx context.Context) (map[string]ggufIndexEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT path,size_bytes,mtime_ns,gguf_version,tensor_count,metadata_count,architecture,
       context_length,block_count,embedding_length,head_count,kv_head_count,key_length,value_length,
       nextn_predict_layers,has_mtp,mtp_only,projector,inspect_error
FROM gguf_index`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]ggufIndexEntry)
	for rows.Next() {
		var (
			path                                                                 string
			entry                                                                ggufIndexEntry
			version, tensorCount, metadataCount, nextN                            int64
			architecture                                                         string
			contextLength, blockCount, embedding, headCount, kvHead, keyLen, valLen int64
			hasMTP, mtpOnly, projector                                            int
		)
		if err := rows.Scan(
			&path, &entry.SizeBytes, &entry.MTimeNS, &version, &tensorCount, &metadataCount, &architecture,
			&contextLength, &blockCount, &embedding, &headCount, &kvHead, &keyLen, &valLen,
			&nextN, &hasMTP, &mtpOnly, &projector, &entry.Warning,
		); err != nil {
			return nil, err
		}
		entry.Summary = ggufmeta.Summary{
			Version:       uint32(version),
			TensorCount:   uint64(tensorCount),
			MetadataCount: uint64(metadataCount),
			Derived: ggufmeta.Derived{
				Architecture:  architecture,
				ContextLength: contextLength,
				BlockCount:    blockCount,
				Embedding:     embedding,
				HeadCount:     headCount,
				KVHeadCount:   kvHead,
				KeyLength:     keyLen,
				ValueLength:   valLen,
			},
			Features: ggufmeta.Features{
				Architecture:       architecture,
				NextNPredictLayers: nextN,
				HasMTP:             hasMTP != 0,
				MTPOnly:            mtpOnly != 0,
				Projector:          projector != 0,
			},
		}
		out[filepath.ToSlash(filepath.Clean(path))] = entry
	}
	return out, rows.Err()
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
	tensorCount, err := ggufIndexUint(entry.Summary.TensorCount)
	if err != nil {
		return err
	}
	metadataCount, err := ggufIndexUint(entry.Summary.MetadataCount)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO gguf_index(
 path,size_bytes,mtime_ns,gguf_version,tensor_count,metadata_count,architecture,
 context_length,block_count,embedding_length,head_count,kv_head_count,key_length,value_length,
 nextn_predict_layers,has_mtp,mtp_only,projector,inspect_error,updated_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,unixepoch())
ON CONFLICT(path) DO UPDATE SET
 size_bytes=excluded.size_bytes,
 mtime_ns=excluded.mtime_ns,
 gguf_version=excluded.gguf_version,
 tensor_count=excluded.tensor_count,
 metadata_count=excluded.metadata_count,
 architecture=excluded.architecture,
 context_length=excluded.context_length,
 block_count=excluded.block_count,
 embedding_length=excluded.embedding_length,
 head_count=excluded.head_count,
 kv_head_count=excluded.kv_head_count,
 key_length=excluded.key_length,
 value_length=excluded.value_length,
 nextn_predict_layers=excluded.nextn_predict_layers,
 has_mtp=excluded.has_mtp,
 mtp_only=excluded.mtp_only,
 projector=excluded.projector,
 inspect_error=excluded.inspect_error,
 updated_at=unixepoch()`,
		path, entry.SizeBytes, entry.MTimeNS, int64(entry.Summary.Version), tensorCount, metadataCount,
		entry.Summary.Derived.Architecture, entry.Summary.Derived.ContextLength, entry.Summary.Derived.BlockCount,
		entry.Summary.Derived.Embedding, entry.Summary.Derived.HeadCount, entry.Summary.Derived.KVHeadCount,
		entry.Summary.Derived.KeyLength, entry.Summary.Derived.ValueLength,
		entry.Summary.Features.NextNPredictLayers, boolInt(entry.Summary.Features.HasMTP),
		boolInt(entry.Summary.Features.MTPOnly), boolInt(entry.Summary.Features.Projector), entry.Warning,
	)
	return err
}

func (s *Service) removeMissingGGUFIndex(ctx context.Context, index map[string]ggufIndexEntry, seen map[string]bool) error {
	for path := range index {
		if seen[path] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM gguf_index WHERE path=?`, path); err != nil {
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
