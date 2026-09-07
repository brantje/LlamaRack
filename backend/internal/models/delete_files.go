package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

var (
	ErrArtifactShared     = errors.New("model artifact is still referenced by another registered Model")
	ErrUnsafeArtifactPath = errors.New("unsafe model artifact path")
	errPathSymlink        = errors.New("path contains a symbolic link")
	removeArtifactFile    = os.Remove
	removeEmptyDirectory  = os.Remove
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

type artifactDirectory struct {
	relativePath  string
	absolutePath  string
	canonicalPath string
}

type artifactReference struct {
	path string
	size int64
}

const companionOptionSQL = "option_key IN ('mmproj','spec-draft-model','draft-model')"

// PrepareFileDeletion resolves the LlamaRack-owned file set for a Model and
// validates every unlink target before lifecycle shutdown begins. Ownership is
// the primary GGUF plus completed download_files rows linked through
// provider_imports. Companion paths that are only referenced in options are
// not deletion targets.
func (s *Service) PrepareFileDeletion(ctx context.Context, id string) (FileDeletePlan, error) {
	model, err := s.GetByID(ctx, id)
	if err != nil {
		return FileDeletePlan{}, err
	}
	refs, err := s.ownedArtifactReferences(ctx, model)
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

// DeleteFilesAndModel revalidates the owned artifact set after the caller has
// stopped all Model Instances, unlinks those exact files, prunes resulting
// empty directories inside the models root, and deletes the database Model
// only after every filesystem operation has succeeded.
func (s *Service) DeleteFilesAndModel(ctx context.Context, id string, plan FileDeletePlan) error {
	if plan.modelID != id {
		return errors.New("file deletion plan does not match Model")
	}
	fresh, err := s.PrepareFileDeletion(ctx, id)
	if err != nil {
		return err
	}
	parents := make(map[string]struct{}, len(fresh.files))
	for _, file := range fresh.files {
		if err := removeArtifactFile(file.absolutePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete model artifact file %q: %w", file.relativePath, err)
		}
		parents[filepath.Dir(file.absolutePath)] = struct{}{}
	}
	_ = s.pruneEmptyAncestorDirs(parents)
	return s.Delete(ctx, id)
}

func (s *Service) ownedArtifactReferences(ctx context.Context, model Model) ([]artifactReference, error) {
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
	defer rows.Close()
	for rows.Next() {
		var ref artifactReference
		if err := rows.Scan(&ref.path, &ref.size); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (s *Service) companionOptionReferences(ctx context.Context, modelID string) ([]artifactReference, error) {
	optionRows, err := s.db.QueryContext(ctx, `
SELECT option_value
FROM model_options
WHERE model_id=? AND `+companionOptionSQL+`
ORDER BY option_key`, modelID)
	if err != nil {
		return nil, err
	}
	defer optionRows.Close()
	var refs []artifactReference
	for optionRows.Next() {
		var value string
		if err := optionRows.Scan(&value); err != nil {
			return nil, err
		}
		if strings.TrimSpace(value) != "" {
			refs = append(refs, artifactReference{path: value})
		}
	}
	return refs, optionRows.Err()
}

func (s *Service) artifactReferences(ctx context.Context, model Model) ([]artifactReference, error) {
	refs, err := s.ownedArtifactReferences(ctx, model)
	if err != nil {
		return nil, err
	}
	options, err := s.companionOptionReferences(ctx, model.ID)
	if err != nil {
		return nil, err
	}
	return append(refs, options...), nil
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
			if target, shared := s.sharedDeletionTarget(targets, ref.path); shared {
				return fmt.Errorf("%w: %q is referenced by Model %q", ErrArtifactShared, target, other.Name)
			}
		}
	}
	return s.ensureInstanceCompanionNotShared(ctx, modelID, targets)
}

func (s *Service) ensureInstanceCompanionNotShared(ctx context.Context, modelID string, targets map[string]string) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT m.name, io.option_value
FROM instance_options io
JOIN instances i ON i.id=io.instance_id
JOIN models m ON m.id=i.model_id
WHERE i.model_id<>? AND `+companionOptionSQL+` AND TRIM(io.option_value)<>''
ORDER BY m.name, io.option_key`, modelID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return err
		}
		if target, shared := s.sharedDeletionTarget(targets, value); shared {
			return fmt.Errorf("%w: %q is referenced by Model %q", ErrArtifactShared, target, name)
		}
	}
	return rows.Err()
}

func (s *Service) sharedDeletionTarget(targets map[string]string, storedPath string) (string, bool) {
	canonical, err := s.referencedCanonicalPath(storedPath)
	if err != nil {
		return "", false
	}
	target, shared := targets[canonical]
	return target, shared
}

func (s *Service) referencedCanonicalPath(storedPath string) (string, error) {
	candidate, err := s.artifactCandidate(storedPath)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(candidate); err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			candidate = resolved
		}
	}
	ancestor, ancestorReal, err := existingAncestor(filepath.Dir(candidate))
	if err != nil {
		return "", err
	}
	suffix, err := filepath.Rel(ancestor, candidate)
	if err != nil {
		return "", err
	}
	canonical := filepath.Clean(filepath.Join(ancestorReal, suffix))
	if !withinRoot(rootReal, canonical) {
		return "", errors.New("referenced artifact path resolves outside configured models directory")
	}
	return canonical, nil
}

func (s *Service) pruneEmptyAncestorDirs(parents map[string]struct{}) error {
	if len(parents) == 0 {
		return nil
	}
	dirs := make([]string, 0, len(parents))
	for dir := range parents {
		dirs = append(dirs, dir)
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) > len(dirs[j])
	})
	for _, dir := range dirs {
		if err := s.pruneEmptyAncestors(dir); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) pruneEmptyAncestors(start string) error {
	root, err := filepath.Abs(s.modelsDir)
	if err != nil {
		return err
	}
	current := filepath.Clean(start)
	for {
		if current == filepath.Clean(root) || !withinRoot(root, current) {
			return nil
		}
		if err := ensureNoSymlinkComponents(root, current); err != nil {
			if errors.Is(err, errPathSymlink) {
				return nil
			}
			return fmt.Errorf("inspect model directory %q: %w", current, err)
		}
		info, err := os.Lstat(current)
		switch {
		case errors.Is(err, os.ErrNotExist):
			current = filepath.Dir(current)
			continue
		case err != nil:
			return fmt.Errorf("inspect model directory %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return fmt.Errorf("read model directory %q: %w", current, err)
		}
		if len(entries) > 0 {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if err := removeEmptyDirectory(current); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				current = filepath.Dir(current)
				continue
			}
			if isNonEmptyDirectoryError(err) {
				return nil
			}
			return fmt.Errorf("delete empty model directory %q: %w", filepath.ToSlash(relative), err)
		}
		current = filepath.Dir(current)
	}
}

func isNonEmptyDirectoryError(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
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
		relativePath:  filepath.ToSlash(relative),
		absolutePath:  directory,
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
			return fmt.Errorf("%w: %q", errPathSymlink, current)
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
	if err := ensureNoSymlinkComponents(root, candidate); err != nil {
		return artifactFile{}, fmt.Errorf("%w: %q: %v", ErrUnsafeArtifactPath, stored, err)
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
