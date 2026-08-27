package models

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/brantje/llamacpp-manager/backend/internal/ggufmeta"
	"github.com/brantje/llamacpp-manager/backend/internal/llamaconfig"
)

// InspectGGUF validates a path through the normal Model path rules and reads
// only the GGUF metadata/header area. Tensor payloads are never loaded.
func (s *Service) InspectGGUF(path string) (ggufmeta.Inspection, error) {
	rel, _, err := s.resolveGGUF(path)
	if err != nil {
		return ggufmeta.Inspection{}, err
	}
	return ggufmeta.Inspect(filepath.Join(s.modelsDir, filepath.FromSlash(rel)))
}

func (s *Service) DetectGGUFFeatures(path string) (ggufmeta.Features, error) {
	rel, _, err := s.resolveGGUF(path)
	if err != nil {
		return ggufmeta.Features{}, err
	}
	return ggufmeta.DetectFeatures(filepath.Join(s.modelsDir, filepath.FromSlash(rel)))
}

// DetectedLlamaDefaults returns conservative runtime defaults for GGUF features
// the manager can prove from the file itself. Explicit global/model/instance
// configuration remains authoritative because llamaconfig applies these first.
func (s *Service) DetectedLlamaDefaults(ctx context.Context, modelID string) (map[string]string, error) {
	model, err := s.GetByID(ctx, modelID)
	if err != nil {
		return nil, err
	}
	features, err := s.DetectGGUFFeatures(model.GGUFPath)
	if err != nil {
		// Pending provider downloads and malformed/unreadable GGUFs simply have no
		// detected defaults. Normal model loading will surface its own error.
		return nil, nil
	}
	mtp := features.HasMTP && !features.MTPOnly
	if !mtp {
		options, optionErr := s.Options(ctx, modelID)
		if optionErr != nil {
			return nil, optionErr
		}
		draftPath := strings.TrimSpace(options["spec-draft-model"])
		if draftPath != "" {
			if draftFeatures, inspectErr := ggufmeta.DetectFeatures(draftPath); inspectErr == nil && draftFeatures.MTPOnly {
				mtp = true
			}
		}
	}
	if !mtp {
		return nil, nil
	}
	return map[string]string{
		"spec-type":        "draft-mtp",
		"spec-draft-n-max": "16",
		"spec-draft-p-min": "0.8",
	}, nil
}

// DetectContext returns the architecture-specific context capability when it is
// present in GGUF metadata. A zero result means the file was readable but did
// not contain a usable context capability.
func (s *Service) DetectContext(path string) (int, error) {
	inspection, err := s.InspectGGUF(path)
	if err != nil {
		return 0, err
	}
	return safeContextInt(inspection.Derived.ContextLength), nil
}

// RefreshDetectedContext fills an unknown stored context capability from the
// backing GGUF. Explicit/non-zero values are never overwritten.
func (s *Service) RefreshDetectedContext(ctx context.Context, id string) (Model, error) {
	model, err := s.GetByID(ctx, id)
	if err != nil {
		return Model{}, err
	}
	if model.ContextLength > 0 {
		return model, nil
	}
	contextLength, err := s.DetectContext(model.GGUFPath)
	if err != nil || contextLength <= 0 {
		return model, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE models SET context_length=?, updated_at=unixepoch() WHERE id=? AND context_length=0`, contextLength, id); err != nil {
		return Model{}, err
	}
	return s.GetByID(ctx, id)
}

// RefreshUnknownContexts is deliberately tolerant: Models may point at pending
// Hugging Face downloads or valid GGUFs without a context key. Those conditions
// are retried later rather than treated as manager failures.
func (s *Service) RefreshUnknownContexts(ctx context.Context) error {
	items, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, model := range items {
		if model.ContextLength > 0 {
			continue
		}
		_, _ = s.RefreshDetectedContext(ctx, model.ID)
	}
	return nil
}

// RunMetadataReconciler also covers Models created before a provider download
// has finished. Once the GGUF becomes available, Context capability is filled
// automatically without provider-specific parsing logic. It also registers the
// header-derived llama.cpp defaults used synchronously by every later launch.
func (s *Service) RunMetadataReconciler(ctx context.Context, interval time.Duration) {
	unregister := llamaconfig.RegisterDetectedDefaultsProvider(s.db, s.DetectedLlamaDefaults)
	defer unregister()
	if interval <= 0 {
		interval = 2 * time.Second
	}
	_ = s.RefreshUnknownContexts(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.RefreshUnknownContexts(ctx)
		}
	}
}

func safeContextInt(value int64) int {
	if value <= 0 {
		return 0
	}
	converted := int(value)
	if int64(converted) != value {
		return 0
	}
	return converted
}
