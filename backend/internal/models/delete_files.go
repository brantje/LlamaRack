package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrArtifactShared      = errors.New("model artifact is still referenced by another registered Model")
	ErrUnsafeArtifactPath  = errors.New("unsafe model artifact path")
	removeArtifactFile     = os.Remove
	removeModelDirectory   = os.RemoveAll
)

type FileDeletePlan struct {
	modelID   string
	files     []artifactFile
	directory *artifactDirectory
}

type artifactFile struct {
	storedPath    string
	relativePath  string
	absolutePath  string
	canonicalPath string
	size          int64
}

type artifactDirectory struct {
	relativePath  string
	absolutePath  string
	canonicalPath string
}

type artifactReference struct {
	path string
	size int64
}

// PrepareFileDeletion resolves the exact persisted file set owned by a Model and
// validates every target before lifecycle shutdown begins. For a Model stored in
// a nested directory it also validates the primary GGUF's parent as the model
// directory. The configured models root is never eligible for recursive removal.
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
	var primary artifactFile
	for index, ref := range refs {
		file, err := s.resolveArtifactFile(ref)
		if err != nil {
			return FileDeletePlan{}, err
		}
		if index == 0 {
			primary = file
		}
		if _, exists := seen[file.canonicalPath]; exists {
			continue
		}
		seen[file.canonicalPath] = struct{}{}
		files = append(files, file)
	}
	directory, err := s.resolveModelDirectory(primary)
	if err != nil {
		return FileDeletePlan{}, err
	}
	plan := FileDeletePlan{modelID: id, files: files, directory: directory}
	if err := s.ensureArtifactNotShared(ctx, id, files, directory); err != nil {
		return FileDeletePlan{}, err
	}
	return plan, nil
}

// DeleteFilesAndModel revalidates the persisted artifact association after the
// caller has stopped all Model Instances, removes exact artifacts outside the
// model directory, recursively removes a safe nested model directory when one
// exists, and deletes the database Model only after every filesystem operation
// has succeeded.
func (s *Service) DeleteFilesAndModel(ctx context.Context, id string, plan FileDeletePlan) error {
	if plan.modelID != id {
		return errors.New("file deletion plan does not match Model")
	}
	fresh, err := s.PrepareFileDeletion(ctx, id)
	if err != nil {
		return err
	}
	for _, file := range fresh.files {
		if fresh.directory != nil && withinRoot(fresh.directory.absolutePath, file.absolutePath) {
			continue
		}
		if err := removeArtifactFile(file.absolutePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete model artifact file %q: %w", file.relativePath, err)
		}
	}
	if fresh.directory != nil {
		if err := removeModelDirectory(fresh.directory.absolutePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete model directory %q: %w", fresh.directory.relativePath, err)
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

func (s *Service) ensureArtifactNotShared(ctx context.Context, modelID string, files []artifactFile, directory *artifactDirectory) error {
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
			if directory != nil {
				candidate, err := s.artifactCandidate(ref.path)
				if err == nil && withinRoot(directory.absolutePath, candidate) {
					return fmt.Errorf("%w: model directory %q contains an artifact referenced by Model %q", ErrArtifactShared, directory.relativePath, other.Name)
				}
			}
			file, err := s.resolveArtifactFile(ref)
			if err != nil {
				// A malformed artifact owned by an unrelated Model must not make a
				// valid Model impossible to delete unless its stored path points into
				// the directory that would be recursively removed (handled above).
				continue
			}
			if target, shared := targets[file.canonicalPath]; shared {
				return fmt.Errorf("%w: %q is referenced by Model %q", ErrArtifactShared, target, other.Name)
			}
		}
	}
	return nil
}

func (s *Service) artifactCandidate(storedPath string) (string, error) {
	stored := strings.TrimSpace(storedPath)
	if stored == "" {
		return "", errors.New("empty artifact file path")
	}
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return "", err
	}
	candidate := filepath.FromSlash(stored)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if !withinRoot(root, candidate) {
		return "", errors.New("artifact path escapes configured models directory")
	}
	return candidate, nil
}

func (s *Service) resolveModelDirectory(primary artifactFile) (*artifactDirectory, error) {
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return nil, err
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve models directory: %w", err)
	}
	directory := filepath.Clean(filepath.Dir(primary.absolutePath))
	if directory == filepath.Clean(root) {
		return nil, nil
	}
	if !withinRoot(root, directory) {
		return nil, fmt.Errorf("%w: model directory escapes configured models directory", ErrUnsafeArtifactPath)
	}
	if err := ensureNoSymlinkComponents(root, directory); err != nil {
		return nil, fmt.Errorf("%w: model directory %q: %v", ErrUnsafeArtifactPath, directory, err)
	}
	ancestor, ancestorReal, err := existingAncestor(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve model directory: %w", err)
	}
	suffix, err := filepath.Rel(ancestor, directory)
	if err != nil {
		return nil, err
	}
	canonical := filepath.Clean(filepath.Join(ancestorReal, suffix))
	if canonical == filepath.Clean(rootReal) || !withinRoot(rootReal, canonical) {
		return nil, fmt.Errorf("%w: model directory resolves outside configured models directory", ErrUnsafeArtifactPath)
	}
	if info, err := os.Lstat(directory); err == nil {
		if !info.IsDir() {
			return nil, fmt.Errorf("%w: model directory %q is not a directory", ErrUnsafeArtifactPath, directory)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect model directory %q: %w", directory, err)
	}
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return nil, err
	}
	return &artifactDirectory{
		relativePath: filepath.ToSlash(relative),
		absolutePath: directory,
		canonicalPath: canonical,
	}, nil
}

func ensureNoSymlinkComponents(root, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return err
	}
	if relative == "." {
		return nil
	}
	current := filepath.Clean(root)
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%q is a symbolic link", current)
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
	candidate, err := s.artifactCandidate(stored)
	if err != nil {
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
