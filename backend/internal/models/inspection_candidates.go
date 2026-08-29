package models

import (
	"context"
	"os"
	"path/filepath"
	"sort"
)

// GGUFArtifactDependencyCandidate exposes every complete metadata-classified
// companion artifact in the same inspection scope. The selected dependency
// remains available through GGUFInspection.Dependencies; these candidates let
// callers offer explicit alternate choices without relying on filenames.
type GGUFArtifactDependencyCandidate struct {
	Kind         string             `json:"kind"`
	Name         string             `json:"name"`
	Quantization string             `json:"quantization,omitempty"`
	TotalBytes   int64              `json:"total_bytes"`
	Files        []GGUFArtifactFile `json:"files"`
	OptionPath   string             `json:"option_path"`
}

// InspectGGUFArtifactCandidates returns all complete projector and MTP helper
// artifacts that belong to the exact same scope used by InspectGGUFArtifact.
// Helper kinds come from GGUF metadata, never from filenames.
func (s *Service) InspectGGUFArtifactCandidates(ctx context.Context, path string) ([]GGUFArtifactDependencyCandidate, error) {
	mainRel, _, err := s.resolveGGUF(path)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return nil, err
	}

	scopePaths, err := s.localArtifactScopePaths(ctx, root, mainRel)
	if err != nil {
		return nil, err
	}
	groups := buildLocalArtifactGroups(root, scopePaths)
	mainKey, _, _, _, ok := localArtifactIdentity(mainRel)
	if !ok {
		return nil, os.ErrInvalid
	}
	mainGroup := groups[mainKey]
	if mainGroup == nil {
		groups = buildLocalArtifactGroups(root, []string{mainRel})
		mainGroup = groups[mainKey]
	}
	if mainGroup == nil {
		return nil, os.ErrNotExist
	}
	if !mainGroup.complete() {
		return nil, nil
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	candidates := make([]GGUFArtifactDependencyCandidate, 0)
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
		absolute, ok := sidecarAbsolutePath(root, firstRel)
		if !ok {
			continue
		}
		candidates = append(candidates, GGUFArtifactDependencyCandidate{
			Kind:         kind,
			Name:         group.name,
			Quantization: quantFromName(group.name),
			TotalBytes:   group.totalBytes(),
			Files:        group.fileSlice(),
			OptionPath:   absolute,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Kind != candidates[j].Kind {
			return candidates[i].Kind < candidates[j].Kind
		}
		return candidates[i].Name < candidates[j].Name
	})
	return candidates, nil
}
