package models

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
)

// GGUFArtifactFile mirrors the provider artifact file shape for local GGUFs.
type GGUFArtifactFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// GGUFArtifactDependency mirrors the companion-artifact shape returned by the
// Hugging Face provider while keeping local classification metadata-driven.
type GGUFArtifactDependency struct {
	Kind         string             `json:"kind"`
	Name         string             `json:"name"`
	Quantization string             `json:"quantization,omitempty"`
	TotalBytes   int64              `json:"total_bytes"`
	Files        []GGUFArtifactFile `json:"files"`
}

// GGUFInspection intentionally contains the same logical artifact fields used
// by Hugging Face discovery. Local-only metadata is appended so the Model form
// can use one inspection response for helper selection and context detection.
type GGUFInspection struct {
	ID             string                   `json:"id"`
	Name           string                   `json:"name"`
	Quantization   string                   `json:"quantization,omitempty"`
	ModelBytes     int64                    `json:"model_bytes"`
	TotalBytes     int64                    `json:"total_bytes"`
	ShardCount     int                      `json:"shard_count"`
	ExpectedShards int                      `json:"expected_shards"`
	Complete       bool                     `json:"complete"`
	Files          []GGUFArtifactFile       `json:"files"`
	Dependencies   []GGUFArtifactDependency `json:"dependencies,omitempty"`

	ModelName        string            `json:"model_name,omitempty"`
	Architecture     string            `json:"architecture,omitempty"`
	ContextLength    int64             `json:"context_length,omitempty"`
	GGUFVersion      uint32            `json:"gguf_version,omitempty"`
	MetadataCount    uint64            `json:"metadata_count,omitempty"`
	Warning          string            `json:"warning,omitempty"`
	Features         ggufmeta.Features `json:"features,omitempty"`
	SuggestedOptions map[string]string `json:"suggested_options,omitempty"`
}

type localArtifactGroup struct {
	key      string
	dir      string
	name     string
	expected int
	files    map[int]GGUFArtifactFile
	rels     map[int]string
}

type localDependencyCandidate struct {
	dependency GGUFArtifactDependency
	optionPath string
}

// InspectGGUFArtifact resolves one local selectable path into the same artifact
// model returned by Hugging Face discovery. When the path belongs to a completed
// provider download, only files from that exact download job are considered for
// projector/MTP relationships. Otherwise the containing directory is treated as
// the artifact scope. Helper classification always comes from GGUF metadata.
func (s *Service) InspectGGUFArtifact(ctx context.Context, path string) (GGUFInspection, error) {
	mainRel, _, err := s.resolveGGUF(path)
	if err != nil {
		return GGUFInspection{}, err
	}
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return GGUFInspection{}, err
	}

	scopePaths, err := s.localArtifactScopePaths(ctx, root, mainRel)
	if err != nil {
		return GGUFInspection{}, err
	}
	groups := buildLocalArtifactGroups(root, scopePaths)
	mainKey, _, _, _, ok := localArtifactIdentity(mainRel)
	if !ok {
		return GGUFInspection{}, os.ErrInvalid
	}
	mainGroup := groups[mainKey]
	if mainGroup == nil {
		// The selected file may be the only path even if a concurrent directory
		// change happened after resolveGGUF. Keep the response useful in that case.
		groups = buildLocalArtifactGroups(root, []string{mainRel})
		mainGroup = groups[mainKey]
	}
	if mainGroup == nil {
		return GGUFInspection{}, os.ErrNotExist
	}

	mainFiles := mainGroup.fileSlice()
	inspection := GGUFInspection{
		ID:             mainGroup.logicalPath(),
		Name:           mainGroup.name,
		Quantization:   quantFromName(mainGroup.name),
		ModelBytes:     mainGroup.totalBytes(),
		ShardCount:     len(mainFiles),
		ExpectedShards: mainGroup.expected,
		Complete:       mainGroup.complete(),
		Files:          append([]GGUFArtifactFile(nil), mainFiles...),
	}
	inspection.TotalBytes = inspection.ModelBytes

	summary, summaryErr := s.GGUFSummary(ctx, mainRel)
	if summaryErr != nil {
		inspection.Warning = summaryErr.Error()
		return inspection, nil
	}
	inspection.Architecture = summary.Derived.Architecture
	inspection.ContextLength = summary.Derived.ContextLength
	inspection.GGUFVersion = summary.Version
	inspection.MetadataCount = summary.MetadataCount
	inspection.Features = summary.Features
	if metadataRel := mainGroup.firstRel(); metadataRel != "" {
		namePage, nameErr := ggufmeta.ReadValuePage(filepath.Join(root, filepath.FromSlash(metadataRel)), "general.name", 0, 0)
		if nameErr == nil && namePage.Type == "string" {
			inspection.ModelName = strings.TrimSpace(namePage.Value)
		}
	}

	options := map[string]string{}
	if summary.Features.HasMTP && !summary.Features.MTPOnly {
		applyMTPDefaults(options)
	}
	if !inspection.Complete {
		if len(options) != 0 {
			inspection.SuggestedOptions = options
		}
		return inspection, nil
	}

	candidates := map[string][]localDependencyCandidate{"mmproj": {}, "mtp": {}}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == mainKey {
			continue
		}
		group := groups[key]
		if group == nil || !group.complete() {
			continue
		}
		firstRel := group.firstRel()
		if firstRel == "" {
			continue
		}
		helperSummary, helperErr := s.GGUFSummary(ctx, firstRel)
		if helperErr != nil {
			continue
		}
		kind := ""
		switch {
		case helperSummary.Features.Projector:
			kind = "mmproj"
		case helperSummary.Features.MTPOnly:
			kind = "mtp"
		default:
			continue
		}
		files := group.fileSlice()
		absolute := filepath.Join(root, filepath.FromSlash(firstRel))
		candidates[kind] = append(candidates[kind], localDependencyCandidate{
			dependency: GGUFArtifactDependency{
				Kind:         kind,
				Name:         group.name,
				Quantization: quantFromName(group.name),
				TotalBytes:   group.totalBytes(),
				Files:        files,
			},
			optionPath: absolute,
		})
	}

	for _, kind := range []string{"mmproj", "mtp"} {
		selected, ok := selectLocalDependency(kind, inspection.Quantization, candidates[kind])
		if !ok {
			continue
		}
		inspection.Dependencies = append(inspection.Dependencies, selected.dependency)
		inspection.Files = append(inspection.Files, selected.dependency.Files...)
		inspection.TotalBytes += selected.dependency.TotalBytes
		switch kind {
		case "mmproj":
			options["mmproj"] = selected.optionPath
		case "mtp":
			options["spec-draft-model"] = selected.optionPath
			applyMTPDefaults(options)
		}
	}
	if len(options) != 0 {
		inspection.SuggestedOptions = options
	}
	return inspection, nil
}

func (s *Service) localArtifactScopePaths(ctx context.Context, root, mainRel string) ([]string, error) {
	mainKey := filepath.ToSlash(filepath.Clean(mainRel))
	associated, err := s.downloadSidecarsByMain(ctx)
	if err != nil {
		return nil, err
	}
	if paths := associated[mainKey]; len(paths) != 0 {
		return append([]string(nil), paths...), nil
	}

	return directorySiblingGGUFs(root, mainRel)
}

func buildLocalArtifactGroups(root string, paths []string) map[string]*localArtifactGroup {
	groups := map[string]*localArtifactGroup{}
	for _, candidate := range paths {
		absolute, ok := sidecarAbsolutePath(root, candidate)
		if !ok {
			continue
		}
		rel, err := filepath.Rel(root, absolute)
		if err != nil {
			continue
		}
		info, err := os.Stat(absolute)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		key, name, part, expected, ok := localArtifactIdentity(rel)
		if !ok {
			continue
		}
		group := groups[key]
		if group == nil {
			group = &localArtifactGroup{
				key: key, dir: filepath.Dir(rel), name: name, expected: expected,
				files: map[int]GGUFArtifactFile{}, rels: map[int]string{},
			}
			groups[key] = group
		}
		if group.expected != expected {
			group.expected = -1
			continue
		}
		group.files[part] = GGUFArtifactFile{Path: filepath.ToSlash(filepath.Clean(rel)), Size: info.Size()}
		group.rels[part] = filepath.Clean(rel)
	}
	return groups
}

func localArtifactIdentity(rel string) (key, name string, part, expected int, ok bool) {
	rel = filepath.Clean(filepath.FromSlash(rel))
	base := filepath.Base(rel)
	match := localSplitPattern.FindStringSubmatch(base)
	if match == nil {
		return filepath.ToSlash(rel), base, 1, 1, true
	}
	part, partErr := strconv.Atoi(match[2])
	expected, expectedErr := strconv.Atoi(match[3])
	if partErr != nil || expectedErr != nil || part <= 0 || expected <= 0 || part > expected {
		return "", "", 0, 0, false
	}
	name = match[1] + ".gguf"
	key = filepath.ToSlash(filepath.Join(filepath.Dir(rel), strings.ToLower(name)))
	return key, name, part, expected, true
}

func (g *localArtifactGroup) complete() bool {
	if g == nil || g.expected <= 0 || len(g.files) != g.expected {
		return false
	}
	for part := 1; part <= g.expected; part++ {
		if _, ok := g.files[part]; !ok {
			return false
		}
	}
	return true
}

func (g *localArtifactGroup) fileSlice() []GGUFArtifactFile {
	if g == nil {
		return nil
	}
	parts := make([]int, 0, len(g.files))
	for part := range g.files {
		parts = append(parts, part)
	}
	sort.Ints(parts)
	out := make([]GGUFArtifactFile, 0, len(parts))
	for _, part := range parts {
		out = append(out, g.files[part])
	}
	return out
}

func (g *localArtifactGroup) firstRel() string {
	if g == nil {
		return ""
	}
	if rel := g.rels[1]; rel != "" {
		return filepath.ToSlash(rel)
	}
	parts := make([]int, 0, len(g.rels))
	for part := range g.rels {
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Ints(parts)
	return filepath.ToSlash(g.rels[parts[0]])
}

func (g *localArtifactGroup) logicalPath() string {
	if g == nil {
		return ""
	}
	if g.dir == "." || g.dir == "" {
		return filepath.ToSlash(g.name)
	}
	return filepath.ToSlash(filepath.Join(g.dir, g.name))
}

func (g *localArtifactGroup) totalBytes() int64 {
	var total int64
	for _, file := range g.files {
		total += file.Size
	}
	return total
}

func selectLocalDependency(kind, targetQuant string, candidates []localDependencyCandidate) (localDependencyCandidate, bool) {
	if len(candidates) == 0 {
		return localDependencyCandidate{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].dependency.Name < candidates[j].dependency.Name
	})
	targetQuant = strings.ToUpper(strings.TrimSpace(targetQuant))
	if targetQuant != "" {
		for _, candidate := range candidates {
			if strings.EqualFold(candidate.dependency.Quantization, targetQuant) {
				return candidate, true
			}
		}
	}
	preferences := []string{"F16", "BF16", "Q8_0", "Q4_K_M", "Q4_0"}
	if kind == "mtp" {
		preferences = []string{"Q4_0", "Q4_K_M", "Q8_0", "F16", "BF16"}
	}
	for _, preferred := range preferences {
		for _, candidate := range candidates {
			if strings.EqualFold(candidate.dependency.Quantization, preferred) {
				return candidate, true
			}
		}
	}
	return candidates[0], true
}
