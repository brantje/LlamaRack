package models

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
)

// LogicalGGUFSize returns the size of the complete logical model artifact.
// Split GGUF Models are represented by their primary shard path, but hardware
// recommendations and Model inventory must account for every shard's weights.
func (s *Service) LogicalGGUFSize(path string) (int64, error) {
	rel, info, err := s.resolveGGUF(path)
	if err != nil {
		return 0, err
	}
	base := filepath.Base(filepath.FromSlash(rel))
	match := localSplitPattern.FindStringSubmatch(base)
	if match == nil {
		return info.Size(), nil
	}
	expected, err := strconv.Atoi(match[3])
	if err != nil || expected <= 0 {
		return 0, errors.New("invalid split GGUF shard count")
	}
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return 0, err
	}
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(rel)))
	ext := filepath.Ext(base)
	var total int64
	for index := 1; index <= expected; index++ {
		name := fmt.Sprintf("%s-%05d-of-%05d%s", match[1], index, expected, ext)
		shard, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return 0, fmt.Errorf("split GGUF shard %d of %d: %w", index, expected, err)
		}
		if !shard.Mode().IsRegular() {
			return 0, fmt.Errorf("split GGUF shard %d of %d is not a regular file", index, expected)
		}
		if shard.Size() > math.MaxInt64-total {
			return 0, errors.New("split GGUF logical size overflows int64")
		}
		total += shard.Size()
	}
	return total, nil
}

// RefreshLogicalSize reconciles the stored Model size with the current logical
// artifact. It is safe to call repeatedly and leaves pending/incomplete split
// downloads untouched when LogicalGGUFSize cannot yet resolve every shard.
func (s *Service) RefreshLogicalSize(ctx context.Context, id string) (Model, error) {
	model, err := s.GetByID(ctx, id)
	if err != nil {
		return Model{}, err
	}
	total, err := s.LogicalGGUFSize(model.GGUFPath)
	if err != nil {
		return model, err
	}
	if total == model.TotalBytes {
		return model, nil
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE models SET total_bytes=?,updated_at=unixepoch() WHERE id=?`, total, id); err != nil {
		return Model{}, err
	}
	return s.GetByID(ctx, id)
}
