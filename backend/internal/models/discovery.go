package models

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
)

type GGUFFile struct {
	Path             string            `json:"path"`
	Name             string            `json:"name"`
	TotalBytes       int64             `json:"total_bytes"`
	ModifiedAt       string            `json:"modified_at,omitempty"`
	Quantization     string            `json:"quantization,omitempty"`
	Architecture     string            `json:"architecture,omitempty"`
	ContextLength    int64             `json:"context_length,omitempty"`
	GGUFVersion      uint32            `json:"gguf_version,omitempty"`
	MetadataCount    uint64            `json:"metadata_count,omitempty"`
	Warning          string            `json:"warning,omitempty"`
	SuggestedOptions map[string]string `json:"suggested_options,omitempty"`
}

type discoveredGGUF struct {
	path    string
	name    string
	size    int64
	mtimeNS int64
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

	index, err := s.loadGGUFIndex(ctx)
	if err != nil {
		return nil, err
	}
	singles := make([]discoveredGGUF, 0)
	splits := map[string]*splitGroup{}
	seen := map[string]bool{}
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
		info, err := entry.Info()
		if err != nil {
			return err
		}
		seen[filepath.ToSlash(rel)] = true
		file := discoveredGGUF{path: rel, name: entry.Name(), size: info.Size(), mtimeNS: info.ModTime().UnixNano()}
		match := localSplitPattern.FindStringSubmatch(entry.Name())
		if match == nil {
			singles = append(singles, file)
			return nil
		}
		part, indexErr := strconv.Atoi(match[2])
		expected, expectedErr := strconv.Atoi(match[3])
		if indexErr != nil || expectedErr != nil || part <= 0 || expected <= 0 || part > expected {
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
		group.files[part] = file
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.removeMissingGGUFIndex(ctx, index, seen); err != nil {
		return nil, err
	}

	sidecarsByMain, err := s.downloadSidecarsByMain(ctx)
	if err != nil {
		return nil, err
	}

	files := make([]GGUFFile, 0, len(singles)+len(splits))
	for _, file := range singles {
		if used[file.path] {
			continue
		}
		summary, warning, err := s.cachedGGUFSummary(ctx, root, file.path, file.size, file.mtimeNS, index)
		if err != nil {
			return nil, err
		}
		if summary.Features.Projector || summary.Features.MTPOnly {
			continue
		}
		suggested, err := s.suggestedSidecarOptions(ctx, root, file.path, summary, index, sidecarsByMain)
		if err != nil {
			return nil, err
		}
		files = append(files, discoveredGGUFFile(file, file.size, summary, warning, suggested))
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
		latestModified := first.mtimeNS
		complete := true
		for part := 1; part <= group.expected; part++ {
			file, exists := group.files[part]
			if !exists {
				complete = false
				break
			}
			total += file.size
			if file.mtimeNS > latestModified {
				latestModified = file.mtimeNS
			}
		}
		if !complete {
			continue
		}
		// Split artifacts are one selectable model. Classification and metadata
		// come from shard 1, so cold discovery no longer parses every shard.
		summary, warning, err := s.cachedGGUFSummary(ctx, root, first.path, first.size, first.mtimeNS, index)
		if err != nil {
			return nil, err
		}
		if summary.Features.Projector || summary.Features.MTPOnly {
			continue
		}
		suggested, err := s.suggestedSidecarOptions(ctx, root, first.path, summary, index, sidecarsByMain)
		if err != nil {
			return nil, err
		}
		first.mtimeNS = latestModified
		files = append(files, discoveredGGUFFile(first, total, summary, warning, suggested))
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func discoveredGGUFFile(file discoveredGGUF, total int64, summary ggufmeta.Summary, warning string, suggested map[string]string) GGUFFile {
	modifiedAt := ""
	if file.mtimeNS > 0 {
		modifiedAt = time.Unix(0, file.mtimeNS).UTC().Format(time.RFC3339Nano)
	}
	return GGUFFile{
		Path:             filepath.ToSlash(file.path),
		Name:             file.name,
		TotalBytes:       total,
		ModifiedAt:       modifiedAt,
		Quantization:     quantFromName(file.name),
		Architecture:     summary.Derived.Architecture,
		ContextLength:    summary.Derived.ContextLength,
		GGUFVersion:      summary.Version,
		MetadataCount:    summary.MetadataCount,
		Warning:          warning,
		SuggestedOptions: suggested,
	}
}

func (s *Service) downloadSidecarsByMain(ctx context.Context) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT j.id, df.local_path
FROM download_jobs j
JOIN download_files df ON df.job_id=j.id
WHERE j.state='COMPLETED' AND df.local_path<>''
ORDER BY j.updated_at DESC, j.id DESC, df.ordinal, df.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]string{}
	var currentJob string
	var currentPaths []string
	flush := func() {
		if currentJob == "" || len(currentPaths) == 0 {
			return
		}
		jobPaths := append([]string(nil), currentPaths...)
		for _, localPath := range currentPaths {
			key := filepath.ToSlash(filepath.Clean(localPath))
			if _, exists := out[key]; !exists {
				out[key] = jobPaths
			}
		}
	}
	for rows.Next() {
		var jobID, localPath string
		if err := rows.Scan(&jobID, &localPath); err != nil {
			return nil, err
		}
		if currentJob != "" && jobID != currentJob {
			flush()
			currentPaths = currentPaths[:0]
		}
		currentJob = jobID
		currentPaths = append(currentPaths, localPath)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	flush()
	return out, nil
}

func (s *Service) suggestedSidecarOptions(ctx context.Context, root, mainPath string, mainSummary ggufmeta.Summary, index map[string]ggufIndexEntry, sidecarsByMain map[string][]string) (map[string]string, error) {
	mainPath = filepath.ToSlash(filepath.Clean(mainPath))
	options := map[string]string{}
	if mainSummary.Features.HasMTP && !mainSummary.Features.MTPOnly {
		applyMTPDefaults(options)
	}

	// Match InspectGGUFArtifact scope: prefer completed download-job paths when
	// present; otherwise scan sibling GGUFs in the same directory so manually
	// placed helpers (e.g. gemma4-assistant MTP + clip projector) still surface
	// as MTP/Vision tags on the available-files list.
	candidates := sidecarsByMain[mainPath]
	if len(candidates) == 0 {
		var err error
		candidates, err = directorySiblingGGUFs(root, mainPath)
		if err != nil {
			return nil, err
		}
	}

	for _, localPath := range candidates {
		absolute, ok := sidecarAbsolutePath(root, localPath)
		if !ok {
			continue
		}
		info, err := os.Stat(absolute)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, absolute)
		if err != nil {
			continue
		}
		summary, warning, err := s.cachedGGUFSummary(ctx, root, rel, info.Size(), info.ModTime().UnixNano(), index)
		if err != nil {
			return nil, err
		}
		if warning != "" {
			continue
		}
		switch {
		case summary.Features.Projector:
			if _, exists := options["mmproj"]; !exists {
				options["mmproj"] = absolute
			}
		case summary.Features.MTPOnly:
			if _, exists := options["spec-draft-model"]; !exists {
				options["spec-draft-model"] = absolute
				applyMTPDefaults(options)
			}
		}
	}
	if len(options) == 0 {
		return nil, nil
	}
	return options, nil
}

func directorySiblingGGUFs(root, mainRel string) ([]string, error) {
	dir := filepath.Dir(filepath.FromSlash(mainRel))
	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
			continue
		}
		paths = append(paths, filepath.ToSlash(filepath.Join(dir, entry.Name())))
	}
	return paths, nil
}

func applyMTPDefaults(options map[string]string) {
	if _, exists := options["spec-type"]; !exists {
		options["spec-type"] = "draft-mtp"
	}
	if _, exists := options["spec-draft-n-max"]; !exists {
		options["spec-draft-n-max"] = "16"
	}
	if _, exists := options["spec-draft-p-min"]; !exists {
		options["spec-draft-p-min"] = "0.8"
	}
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
	summary, err := ggufmeta.ReadSummary(filePath)
	if err != nil {
		return ""
	}
	if summary.Features.Projector {
		return "mmproj"
	}
	if summary.Features.MTPOnly {
		return "mtp"
	}
	return ""
}

func isProjectorGGUF(path string) bool { return localSidecarKind(path) == "mmproj" }
func isMTPGGUF(path string) bool       { return localSidecarKind(path) == "mtp" }
