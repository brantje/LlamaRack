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
	Path             string            `json:"path"`
	Name             string            `json:"name"`
	TotalBytes       int64             `json:"total_bytes"`
	Quantization     string            `json:"quantization,omitempty"`
	SuggestedOptions map[string]string `json:"suggested_options,omitempty"`
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
		if isProjectorGGUF(rel) || isMTPGGUF(rel) {
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
		suggested, err := s.suggestedSidecarOptions(ctx, root, file.path)
		if err != nil {
			return nil, err
		}
		files = append(files, GGUFFile{
			Path: filepath.ToSlash(file.path), Name: file.name, TotalBytes: file.size,
			Quantization: quantFromName(file.name), SuggestedOptions: suggested,
		})
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
		suggested, err := s.suggestedSidecarOptions(ctx, root, first.path)
		if err != nil {
			return nil, err
		}
		files = append(files, GGUFFile{
			Path: filepath.ToSlash(first.path), Name: first.name, TotalBytes: total,
			Quantization: quantFromName(first.name), SuggestedOptions: suggested,
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func (s *Service) suggestedSidecarOptions(ctx context.Context, root, mainPath string) (map[string]string, error) {
	mainPath = filepath.ToSlash(filepath.Clean(mainPath))
	rows, err := s.db.QueryContext(ctx, `
SELECT df.local_path
FROM download_files df
WHERE df.job_id = (
 SELECT f.job_id
 FROM download_files f
 JOIN download_jobs j ON j.id=f.job_id
 WHERE f.local_path=? AND j.state='COMPLETED'
 ORDER BY j.updated_at DESC, j.id DESC
 LIMIT 1
)
ORDER BY df.ordinal, df.path`, mainPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	options := map[string]string{}
	for rows.Next() {
		var localPath string
		if err := rows.Scan(&localPath); err != nil {
			return nil, err
		}
		kind := localSidecarKind(localPath)
		if kind == "" {
			continue
		}
		absolute, ok := sidecarAbsolutePath(root, localPath)
		if !ok {
			continue
		}
		switch kind {
		case "mmproj":
			if _, exists := options["mmproj"]; !exists {
				options["mmproj"] = absolute
			}
		case "mtp":
			if _, exists := options["spec-draft-model"]; !exists {
				options["spec-draft-model"] = absolute
				options["spec-type"] = "draft-mtp"
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(options) == 0 {
		return nil, nil
	}
	return options, nil
}

func sidecarAbsolutePath(root, localPath string) (string, bool) {
	absolute, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(localPath)))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, absolute)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return absolute, true
}

func localSidecarKind(filePath string) string {
	normalized := strings.ToLower(filepath.ToSlash(filePath))
	if strings.Contains(normalized, "mmproj") || strings.Contains(normalized, "mmoproj") || strings.Contains(normalized, "projector") {
		return "mmproj"
	}
	name := strings.ToLower(strings.TrimSuffix(filepath.Base(filepath.FromSlash(filePath)), filepath.Ext(filePath)))
	if name == "mtp" || strings.HasPrefix(name, "mtp-") || strings.HasPrefix(name, "mtp_") || strings.HasPrefix(name, "mtp.") {
		return "mtp"
	}
	return ""
}

func isProjectorGGUF(path string) bool { return localSidecarKind(path) == "mmproj" }
func isMTPGGUF(path string) bool       { return localSidecarKind(path) == "mtp" }
