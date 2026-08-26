package models

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type GGUFFile struct {
	Path         string `json:"path"`
	Name         string `json:"name"`
	TotalBytes   int64  `json:"total_bytes"`
	Quantization string `json:"quantization,omitempty"`
}

func (s *Service) AvailableGGUFs(ctx context.Context) ([]GGUFFile, error) {
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, "SELECT gguf_path FROM models")
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return nil, err
		}
		used[filepath.Clean(filepath.FromSlash(path))] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	files := make([]GGUFFile, 0)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.Clean(rel)
		if used[rel] {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, GGUFFile{
			Path:         filepath.ToSlash(rel),
			Name:         entry.Name(),
			TotalBytes:   info.Size(),
			Quantization: quantFromName(entry.Name()),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}
