package models

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type GGUFFile struct {
	Path         string `json:"path"`
	Name         string `json:"name"`
	TotalBytes   int64  `json:"total_bytes"`
	Quantization string `json:"quantization,omitempty"`
}

type discoveredGGUF struct {
	path string
	name string
	size int64
}

type splitGroup struct {
	expected int
	files    map[int]discoveredGGUF
}

var localSplitPattern = regexp.MustCompile(`(?i)^(.*)-(\d{5})-of-(\d{5})\.gguf$`)

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

	singles := make([]discoveredGGUF, 0)
	splits := map[string]*splitGroup{}
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
		if isProjectorGGUF(rel) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		file := discoveredGGUF{path: rel, name: entry.Name(), size: info.Size()}
		match := localSplitPattern.FindStringSubmatch(entry.Name())
		if match == nil {
			singles = append(singles, file)
			return nil
		}
		index, indexErr := strconv.Atoi(match[2])
		expected, expectedErr := strconv.Atoi(match[3])
		if indexErr != nil || expectedErr != nil || index <= 0 || expected <= 0 || index > expected {
			return nil
		}
		dir := filepath.Dir(rel)
		key := filepath.Join(dir, strings.ToLower(match[1])+".gguf")
		group := splits[key]
		if group == nil {
			group = &splitGroup{expected: expected, files: map[int]discoveredGGUF{}}
			splits[key] = group
		}
		if expected != group.expected {
			group.expected = -1
			return nil
		}
		group.files[index] = file
		return nil
	})
	if err != nil {
		return nil, err
	}

	files := make([]GGUFFile, 0, len(singles)+len(splits))
	for _, file := range singles {
		if used[file.path] {
			continue
		}
		files = append(files, GGUFFile{Path: filepath.ToSlash(file.path), Name: file.name, TotalBytes: file.size, Quantization: quantFromName(file.name)})
	}
	for _, group := range splits {
		if group.expected <= 0 || len(group.files) != group.expected {
			continue
		}
		first, ok := group.files[1]
		if !ok || used[first.path] {
			continue
		}
		var total int64
		complete := true
		for index := 1; index <= group.expected; index++ {
			file, exists := group.files[index]
			if !exists {
				complete = false
				break
			}
			total += file.size
		}
		if !complete {
			continue
		}
		files = append(files, GGUFFile{Path: filepath.ToSlash(first.path), Name: first.name, TotalBytes: total, Quantization: quantFromName(first.name)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func isProjectorGGUF(path string) bool {
	name := strings.ToLower(filepath.ToSlash(path))
	return strings.Contains(name, "mmproj") ||
		strings.Contains(name, "mmoproj") ||
		strings.Contains(name, "projector")
}
