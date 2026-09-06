package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	privateDirPerm  os.FileMode = 0o700
	privateFilePerm os.FileMode = 0o600
)

const permissionRemediation = "place LLAMARACK_DATA_DIR and LLAMARACK_DATABASE_PATH on a Unix filesystem that honors chmod, or fix the volume mount"

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

func isPermissionDenied(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)
}

func isSharedDir(path string, info os.FileInfo) bool {
	if !info.IsDir() {
		return false
	}
	if info.Mode()&os.ModeSticky != 0 {
		return true
	}
	switch filepath.Clean(path) {
	case "/tmp", "/var/tmp", "/dev/shm":
		return true
	default:
		return false
	}
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
	if isSharedDir(path, info) {
		return fmt.Errorf("%s is a sticky shared directory (mode %04o); put LLAMARACK_DATA_DIR and LLAMARACK_DATABASE_PATH in a dedicated LlamaRack data directory instead of a path such as /tmp", path, current)
	}
	if current == perm {
		return nil
	}
	if err := chmod(path, perm); err != nil {
		if info.IsDir() && isPermissionDenied(err) {
			// Bind-mounted data dirs are often owned by the host user. The
			// unprivileged container cannot chmod them; SQLite files created
			// inside the mount are still restricted to 0600.
			return nil
		}
		return fmt.Errorf("restrict permissions on %s: %w; %s", path, err, permissionRemediation)
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
		return fmt.Errorf("%s remains group/world-accessible (mode %04o); %s", path, got, permissionRemediation)
	}
	return fmt.Errorf("%s has mode %04o, want %04o; %s", path, got, perm, permissionRemediation)
}
