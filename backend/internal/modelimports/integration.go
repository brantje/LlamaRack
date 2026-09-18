package modelimports

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/brantje/llamarack/backend/internal/database"

	"github.com/brantje/llamarack/backend/internal/huggingface"
)

// RepairArtifactOptions replaces only manager-generated pending helper paths.
// User-provided llama.cpp option values are deliberately left untouched.
func (s *Service) RepairArtifactOptions(ctx context.Context, modelID, repoID string, artifact huggingface.Artifact) error {
	for _, dependency := range artifact.Dependencies {
		if len(dependency.Files) == 0 {
			continue
		}
		key := ""
		switch dependency.Kind {
		case "mmproj":
			key = "mmproj"
		case "mtp":
			key = "spec-draft-model"
		default:
			continue
		}
		providerPath, err := expectedProviderPathFromRelative(dependency.Files[0].Path)
		if err != nil {
			return err
		}
		correctRel, err := expectedProviderPath(repoID, dependency.Files[0].Path)
		if err != nil {
			return err
		}
		wrongAbs, err := filepath.Abs(filepath.Join(s.modelsDir, filepath.FromSlash(providerPath)))
		if err != nil {
			return err
		}
		correctAbs, err := filepath.Abs(filepath.Join(s.modelsDir, filepath.FromSlash(correctRel)))
		if err != nil {
			return err
		}
		current, found, err := s.store.GetModelOption(ctx, modelID, key)
		if err != nil {
			return err
		}
		switch {
		case !found:
			if err := s.store.InsertModelOption(ctx, modelID, key, correctAbs); err != nil {
				return err
			}
		case filepath.Clean(current) == filepath.Clean(wrongAbs):
			if err := s.store.UpdateModelOption(ctx, modelID, key, correctAbs); err != nil {
				return err
			}
		}
		if dependency.Kind == "mtp" {
			if _, found, err := s.store.GetModelOption(ctx, modelID, "spec-type"); err != nil {
				return err
			} else if !found {
				if err := s.store.InsertModelOption(ctx, modelID, "spec-type", "draft-mtp"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ListResolved reports the import lifecycle maintained by the reconciler rather
// than merely mirroring the underlying download job state. Completed provider
// imports also make one immediate shared-inspector attempt to fill Context
// capability; if it cannot be detected, the completed status carries a visible
// warning instead of silently swallowing the inspection failure.
func (s *Service) ListResolved(ctx context.Context) ([]Status, error) {
	out, err := s.store.List(ctx, true)
	if err != nil {
		return nil, err
	}
	for index := range out {
		s.enrichContextDetectionStatus(ctx, &out[index])
	}
	return out, nil
}

func (s *Service) enrichContextDetectionStatus(ctx context.Context, item *Status) {
	if item == nil || item.State != StateCompleted || item.ModelID == "" {
		return
	}
	model, err := s.models.GetByID(ctx, item.ModelID)
	if err != nil {
		return
	}
	if model.ContextLength > 0 {
		return
	}
	refreshed, err := s.models.RefreshDetectedContext(ctx, item.ModelID)
	if err != nil {
		item.Error = appendImportWarning(item.Error, "Context capability could not be detected automatically: "+err.Error())
		return
	}
	if refreshed.ContextLength <= 0 {
		item.Error = appendImportWarning(item.Error, "Context capability could not be detected automatically from GGUF metadata. Enter it manually on the Model edit page.")
	}
}

func appendImportWarning(existing, warning string) string {
	existing = strings.TrimSpace(existing)
	warning = strings.TrimSpace(warning)
	if existing == "" {
		return warning
	}
	if warning == "" || strings.Contains(existing, warning) {
		return existing
	}
	return existing + "; " + warning
}

// CleanupJobSafe removes Instances created by a pending provider import and
// removes Models only when that import created the Model. Existing Models are
// preserved. Import rows can also survive a user's manual Model deletion as a
// tombstone so a completed download is not immediately auto-registered again.
func (s *Service) CleanupJobSafe(ctx context.Context, jobID string) error {
	items, err := s.store.Linked(ctx, jobID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.InstanceID == "" {
			continue
		}
		if err := s.instances.Delete(ctx, item.InstanceID); err != nil && !errors.Is(err, database.ErrNotFound) && !errors.Is(err, database.ErrNotFound) {
			return err
		}
	}
	if err := s.store.DeleteByJob(ctx, jobID); err != nil {
		return err
	}
	for _, item := range items {
		if !item.OwnsModel || item.ModelID == "" {
			continue
		}
		if err := s.models.Delete(ctx, item.ModelID); err != nil && !errors.Is(err, database.ErrNotFound) && !errors.Is(err, database.ErrNotFound) {
			return err
		}
	}
	return nil
}
