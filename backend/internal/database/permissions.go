package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	privateDirPerm  os.FileMode = 0o700
	privateFilePerm os.FileMode = 0o600
)

type fileStatFunc func(name string) (os.FileInfo, error)
type chmodFunc func(name string, mode os.FileMode) error

// EnsurePrivateDir creates path if needed and restricts it to 0700.
// It does not change "." or the filesystem root.
func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, privateDirPerm); err != nil {
		return err
	}
	if !shouldRestrictDir(path) {
		return nil
	}
	return restrictMode(path, privateDirPerm, false)
}

func shouldRestrictDir(path string) bool {
	cleaned := filepath.Clean(path)
	return cleaned != "." && cleaned != string(os.PathSeparator)
}

func restrictSQLiteFiles(path string) error {
	if err := restrictMode(path, privateFilePerm, false); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := restrictMode(path+suffix, privateFilePerm, true); err != nil {
			return err
		}
	}
	return nil
}

func restrictMode(path string, perm os.FileMode, optional bool) error {
	return restrictModeWith(path, perm, optional, os.Stat, os.Chmod)
}

func restrictModeWith(path string, perm os.FileMode, optional bool, stat fileStatFunc, chmod chmodFunc) error {
	if perm&0o077 != 0 {
		return fmt.Errorf("refused to apply group/world bits to %s (mode %04o)", path, perm)
	}
	info, err := stat(path)
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	current := info.Mode().Perm()
	if current == perm {
		return nil
	}
	if err := chmod(path, perm); err != nil {
		return fmt.Errorf("restrict permissions on %s: %w; place LLAMARACK_DATABASE_PATH on a Unix filesystem that honors chmod, or fix the volume mount", path, err)
	}
	info, err = stat(path)
	if err != nil {
		return err
	}
	got := info.Mode().Perm()
	if got == perm {
		return nil
	}
	if got&0o077 != 0 {
		return fmt.Errorf("%s remains group/world-accessible (mode %04o); place LLAMARACK_DATABASE_PATH on a Unix filesystem that honors chmod, or fix the volume mount", path, got)
	}
	return fmt.Errorf("%s has mode %04o, want %04o; place LLAMARACK_DATABASE_PATH on a Unix filesystem that honors chmod, or fix the volume mount", path, got, perm)
}
