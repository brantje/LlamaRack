package downloads

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const tempFileMode = 0o644

var (
	errDownloadExceedsLimit = errors.New("download exceeds configured max_download_bytes")
	errInvalidDownloadLimit = errors.New("invalid max_download_bytes")
	errTempNotRegular       = errors.New("download temporary file is not a regular file")
	errTempPathEscaped      = errors.New("download temporary path escaped models directory")
)

var openPartial = openPartialFile
var removeFile = os.Remove

func openPartialFile(path string, offset int64) (*os.File, error) {
	var (
		file *os.File
		err  error
	)
	if offset > 0 {
		file, err = os.OpenFile(path, existingWriteFlags(true), 0)
	} else {
		file, err = os.OpenFile(path, reopenWriteFlags(), tempFileMode)
		if errors.Is(err, os.ErrNotExist) {
			file, err = os.OpenFile(path, exclusiveCreateFlags(), tempFileMode)
		}
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, errTempNotRegular
	}
	return file, nil
}

func (m *Manager) maxDownloadBytes(ctx context.Context) (int64, error) {
	if m.limit == nil {
		return defaultMaxDownloadBytes, nil
	}
	n, err := m.limit(ctx)
	if err != nil {
		return 0, err
	}
	if n < 1 {
		return 0, errInvalidDownloadLimit
	}
	return n, nil
}

func legacyPartPath(finalPath, jobID string) string {
	return finalPath + ".lcm-" + jobID + ".part"
}

func randomPartPath(finalPath string) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	return finalPath + ".lcm-" + id + ".part", nil
}

func (m *Manager) modelsRoot() (string, error) {
	return filepath.Abs(m.modelsDir)
}

func (m *Manager) containedPath(path string) (string, error) {
	root, err := m.modelsRoot()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if abs == root || !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", errTempPathEscaped
	}
	return abs, nil
}

func (m *Manager) absTempPath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errTempPathEscaped
	}
	root, err := m.modelsRoot()
	if err != nil {
		return "", err
	}
	return m.containedPath(filepath.Join(root, filepath.FromSlash(rel)))
}

func (m *Manager) persistTempPath(ctx context.Context, jobID, providerPath, absTemp string) error {
	rel := relativeSlash(m.modelsDir, absTemp)
	if rel == "" || strings.HasPrefix(rel, "../") {
		return errTempPathEscaped
	}
	_, err := m.db.ExecContext(ctx, "UPDATE download_files SET temp_path=? WHERE job_id=? AND path=?", rel, jobID, providerPath)
	return err
}

func (m *Manager) clearTempPath(ctx context.Context, jobID, providerPath string) error {
	_, err := m.db.ExecContext(ctx, "UPDATE download_files SET temp_path='' WHERE job_id=? AND path=?", jobID, providerPath)
	return err
}

func regularFileSize(path string) (int64, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if !info.Mode().IsRegular() {
		return 0, false, nil
	}
	return info.Size(), true, nil
}

func (m *Manager) unlinkRegularTemp(path string) error {
	if _, err := m.containedPath(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return removeFile(path)
}

func (m *Manager) createExclusiveTemp(finalPath string) (string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		path, err := randomPartPath(finalPath)
		if err != nil {
			return "", err
		}
		if _, err := m.containedPath(path); err != nil {
			return "", err
		}
		file, err := os.OpenFile(path, exclusiveCreateFlags(), tempFileMode)
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue
			}
			return "", err
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return "", err
		}
		return path, nil
	}
	return "", errors.New("unable to allocate exclusive download temporary file")
}

func (m *Manager) resolveTempFile(ctx context.Context, job Job, file File, finalPath string, restart bool) (string, int64, error) {
	if restart {
		if file.TempPath != "" {
			if abs, err := m.absTempPath(file.TempPath); err == nil {
				_ = m.unlinkRegularTemp(abs)
			}
		}
		_ = m.unlinkRegularTemp(legacyPartPath(finalPath, job.ID))
		path, err := m.createExclusiveTemp(finalPath)
		if err != nil {
			return "", 0, err
		}
		if err := m.persistTempPath(ctx, job.ID, file.Path, path); err != nil {
			_ = m.unlinkRegularTemp(path)
			return "", 0, err
		}
		return path, 0, nil
	}

	if file.TempPath != "" {
		abs, err := m.absTempPath(file.TempPath)
		if err != nil {
			return "", 0, err
		}
		size, regular, err := regularFileSize(abs)
		if err != nil {
			return "", 0, err
		}
		if !regular {
			if _, lerr := os.Lstat(abs); lerr == nil {
				return "", 0, errTempNotRegular
			}
			return m.resolveTempFile(ctx, job, File{Path: file.Path}, finalPath, true)
		}
		return abs, size, nil
	}

	legacy := legacyPartPath(finalPath, job.ID)
	size, regular, err := regularFileSize(legacy)
	if err != nil {
		return "", 0, err
	}
	if regular {
		if err := m.persistTempPath(ctx, job.ID, file.Path, legacy); err != nil {
			return "", 0, err
		}
		return legacy, size, nil
	}
	return m.resolveTempFile(ctx, job, file, finalPath, true)
}

func (m *Manager) jobDownloadedExcept(ctx context.Context, jobID, providerPath string) (int64, error) {
	var total sql.NullInt64
	err := m.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(downloaded_bytes),0) FROM download_files WHERE job_id=? AND path<>?", jobID, providerPath).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

func (m *Manager) removePartial(job Job, file File) error {
	seen := map[string]struct{}{}
	if file.TempPath != "" {
		if abs, err := m.absTempPath(file.TempPath); err == nil {
			seen[abs] = struct{}{}
		}
	}
	if finalPath, err := m.localPath(job, file.Path); err == nil {
		seen[legacyPartPath(finalPath, job.ID)] = struct{}{}
	}
	for path := range seen {
		if err := m.unlinkRegularTemp(path); err != nil {
			return err
		}
	}
	return nil
}
