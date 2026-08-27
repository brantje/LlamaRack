package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrArtifactShared    = errors.New("model artifact is still referenced by another registered Model")
	ErrUnsafeArtifactPath = errors.New("unsafe model artifact path")
)

type FileDeletePlan struct {
	modelID string
	files   []artifactFile
}

type artifactFile struct {
	storedPath    string
	relativePath  string
	absolutePath  string
	canonicalPath string
	size          int64
}

type artifactReference struct {
	path string
	size int64
}

// PrepareFileDeletion resolves the exact persisted file set owned by a Model and
// validates every target before lifecycle shutdown begins. It never expands a
// filename glob and never treats the parent directory as part of the artifact.
func (s *Service) PrepareFileDeletion(ctx context.Context, id string) (FileDeletePlan, error) {
	model, err := s.GetByID(ctx, id)
	if err != nil {
		return FileDeletePlan{}, err
	}
	refs, err := s.artifactReferences(ctx, model)
	if err != nil {
		return FileDeletePlan{}, err
	}
	files := make([]artifactFile, 0, len(refs))
	seen := map[string]struct{}{}
	for _, ref := range refs {
		file, err := s.resolveArtifactFile(ref)
		if err != nil {
			return FileDeletePlan{}, err
		}
		if _, exists := seen[file.canonicalPath]; exists {
			continue
		}
		seen[file.canonicalPath] = struct{}{}
		files = append(files, file)
	}
	plan := FileDeletePlan{modelID: id, files: files}
	if err := s.ensureArtifactNotShared(ctx, id, files); err != nil {
		return FileDeletePlan{}, err
	}
	return plan, nil
}

// DeleteFilesAndModel revalidates the persisted artifact association after the
// caller has stopped all Model Instances, removes exact files, and deletes the
// database Model only after every filesystem operation has succeeded.
func (s *Service) DeleteFilesAndModel(ctx context.Context, id string, plan FileDeletePlan) error {
	if plan.modelID != id {
		return errors.New("file deletion plan does not match Model")
	}
	fresh, err := s.PrepareFileDeletion(ctx, id)
	if err != nil {
		return err
	}
	for _, file := range fresh.files {
		if err := os.Remove(file.absolutePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete model artifact file %q: %w", file.relativePath, err)
		}
	}
	return s.Delete(ctx, id)
}

func (s *Service) artifactReferences(ctx context.Context, model Model) ([]artifactReference, error) {
	refs := []artifactReference{{path: model.GGUFPath, size: model.TotalBytes}}

	rows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT df.local_path,df.size
FROM provider_imports pi
JOIN download_files df ON df.job_id=pi.job_id
WHERE pi.model_id=? AND df.state='COMPLETED' AND TRIM(df.local_path)<>''
ORDER BY df.ordinal,df.path`, model.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ref artifactReference
		if err := rows.Scan(&ref.path, &ref.size); err != nil {
			_ = rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	optionRows, err := s.db.QueryContext(ctx, `
SELECT option_value
FROM model_options
WHERE model_id=? AND option_key IN ('mmproj','spec-draft-model','draft-model')
ORDER BY option_key`, model.ID)
	if err != nil {
		return nil, err
	}
	for optionRows.Next() {
		var value string
		if err := optionRows.Scan(&value); err != nil {
			_ = optionRows.Close()
			return nil, err
		}
		if strings.TrimSpace(value) != "" {
			refs = append(refs, artifactReference{path: value})
		}
	}
	if err := optionRows.Close(); err != nil {
		return nil, err
	}
	return refs, nil
}

func (s *Service) ensureArtifactNotShared(ctx context.Context, modelID string, files []artifactFile) error {
	if len(files) == 0 {
		return nil
	}
	targets := make(map[string]string, len(files))
	for _, file := range files {
		targets[file.canonicalPath] = file.relativePath
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+modelColumns+` FROM models WHERE id<>? ORDER BY name`, modelID)
	if err != nil {
		return err
	}
	var others []Model
	for rows.Next() {
		model, err := scanModel(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		others = append(others, model)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, other := range others {
		refs, err := s.artifactReferences(ctx, other)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			file, err := s.resolveArtifactFile(ref)
			if err != nil {
				// A malformed artifact owned by an unrelated Model must not make a
				// valid Model impossible to delete; it also cannot be a validated
				// shared target.
				continue
			}
			if target, shared := targets[file.canonicalPath]; shared {
				return fmt.Errorf("%w: %q is referenced by Model %q", ErrArtifactShared, target, other.Name)
			}
		}
	}
	return nil
}

func (s *Service) resolveArtifactFile(ref artifactReference) (artifactFile, error) {
	stored := strings.TrimSpace(ref.path)
	if stored == "" {
		return artifactFile{}, fmt.Errorf("%w: empty artifact file path", ErrUnsafeArtifactPath)
	}
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return artifactFile{}, err
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return artifactFile{}, fmt.Errorf("resolve models directory: %w", err)
	}
	candidate := filepath.FromSlash(stored)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return artifactFile{}, err
	}
	if !withinRoot(root, candidate) {
		return artifactFile{}, fmt.Errorf("%w: %q escapes configured models directory", ErrUnsafeArtifactPath, stored)
	}
	if !strings.EqualFold(filepath.Ext(candidate), ".gguf") {
		return artifactFile{}, fmt.Errorf("%w: %q is not a GGUF file", ErrUnsafeArtifactPath, stored)
	}

	ancestor, ancestorReal, err := existingAncestor(filepath.Dir(candidate))
	if err != nil {
		return artifactFile{}, fmt.Errorf("resolve artifact parent for %q: %w", stored, err)
	}
	suffix, err := filepath.Rel(ancestor, candidate)
	if err != nil {
		return artifactFile{}, err
	}
	canonical := filepath.Clean(filepath.Join(ancestorReal, suffix))
	if !withinRoot(rootReal, canonical) {
		return artifactFile{}, fmt.Errorf("%w: %q resolves outside configured models directory", ErrUnsafeArtifactPath, stored)
	}

	size := ref.size
	info, err := os.Lstat(candidate)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return artifactFile{}, fmt.Errorf("%w: %q is a symbolic link", ErrUnsafeArtifactPath, stored)
		}
		if !info.Mode().IsRegular() {
			return artifactFile{}, fmt.Errorf("%w: %q is not a regular file", ErrUnsafeArtifactPath, stored)
		}
		size = info.Size()
	case errors.Is(err, os.ErrNotExist):
		// A missing exact file is already deleted and is therefore safe to
		// continue with. Its existing ancestor was still canonicalized above.
	default:
		return artifactFile{}, fmt.Errorf("inspect model artifact file %q: %w", stored, err)
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return artifactFile{}, err
	}
	return artifactFile{
		storedPath: stored, relativePath: filepath.ToSlash(rel), absolutePath: candidate,
		canonicalPath: canonical, size: size,
	}, nil
}

func existingAncestor(path string) (string, string, error) {
	current := filepath.Clean(path)
	for {
		real, err := filepath.EvalSymlinks(current)
		if err == nil {
			return current, real, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", "", err
		}
		current = parent
	}
}

func withinRoot(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Compile-time guard: sql remains intentionally imported here because callers
// use sql.ErrNoRows as the stable not-found error returned by GetByID.
var _ = sql.ErrNoRows
