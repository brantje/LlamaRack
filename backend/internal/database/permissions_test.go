package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsurePrivateDirCreatesAndRestricts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "config")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dir, 0o700)
}

func TestEnsurePrivateDirSkipsDotAndRoot(t *testing.T) {
	if !shouldRestrictDir("/config") {
		t.Fatal("expected /config to be restricted")
	}
	if shouldRestrictDir(".") {
		t.Fatal("did not expect to restrict .")
	}
	if shouldRestrictDir("/") {
		t.Fatal("did not expect to restrict /")
	}
	if shouldRestrictDir("") {
		t.Fatal("did not expect to restrict empty path")
	}
}

func TestRestrictModeRepairsPermissiveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.db")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := restrictMode(path, privateFilePerm, false); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o600)
}

func TestRestrictModeDoesNotBroadenPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.db")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restrictMode(path, privateFilePerm, false); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o600)
}

func TestRestrictModeSkipsMissingOptionalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db-wal")
	if err := restrictMode(path, privateFilePerm, true); err != nil {
		t.Fatal(err)
	}
}

func TestRestrictModeRequiredMissingFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if err := restrictMode(path, privateFilePerm, false); err == nil {
		t.Fatal("expected missing required file to fail")
	}
}

func TestRestrictModeRefusesGroupWorldTarget(t *testing.T) {
	err := restrictModeWith("x", 0o644, false, os.Stat, os.Chmod)
	if err == nil || !strings.Contains(err.Error(), "group/world bits") {
		t.Fatalf("expected refused group/world bits, got %v", err)
	}
}

func TestRestrictModeFailsWhenChmodRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.db")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := restrictModeWith(path, privateFilePerm, false, os.Stat, func(string, os.FileMode) error {
		return errors.New("operation not permitted")
	})
	if err == nil {
		t.Fatal("expected chmod refusal")
	}
	if !strings.Contains(err.Error(), "honors chmod") {
		t.Fatalf("expected actionable chmod error, got %v", err)
	}
}

func TestRestrictModeFailsWhenFilesystemIgnoresChmod(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.db")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := restrictModeWith(path, privateFilePerm, false, os.Stat, func(string, os.FileMode) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected ignored chmod to fail")
	}
	if !strings.Contains(err.Error(), "group/world-accessible") {
		t.Fatalf("expected group/world-accessible error, got %v", err)
	}
}

func TestRestrictModeFailsWhenPostStatModeMismatchesWithoutGroupWorld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.db")
	if err := os.WriteFile(path, []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := restrictModeWith(path, privateFilePerm, false, func(name string) (os.FileInfo, error) {
		return os.Stat(name)
	}, func(string, os.FileMode) error {
		calls++
		return nil
	})
	if err == nil {
		t.Fatal("expected mode mismatch to fail")
	}
	if !strings.Contains(err.Error(), "want 0600") {
		t.Fatalf("expected want 0600 mismatch, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("chmod calls=%d", calls)
	}
}

func TestRestrictSQLiteFilesOptionalSidecars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.db")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := restrictSQLiteFiles(path); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode=%04o want=%04o", path, got, want)
	}
}

func TestRestrictModeRejectsStickyDirectory(t *testing.T) {
	info := fakeDirInfo(os.ModeDir | os.ModeSticky | 0o777)
	err := restrictModeWith("/var/data", privateDirPerm, false, func(string) (os.FileInfo, error) {
		return info, nil
	}, func(string, os.FileMode) error {
		t.Fatal("must not chmod a sticky directory")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "sticky shared directory") {
		t.Fatalf("expected sticky shared directory error, got %v", err)
	}
}

func TestRestrictModeRejectsStickyPrivateDirectory(t *testing.T) {
	info := fakeDirInfo(os.ModeDir | os.ModeSticky | 0o700)
	err := restrictModeWith("/var/data", privateDirPerm, false, func(string) (os.FileInfo, error) {
		return info, nil
	}, func(string, os.FileMode) error {
		t.Fatal("must not chmod a sticky 0700 directory")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "sticky shared directory") {
		t.Fatalf("expected sticky 01700 directory error, got %v", err)
	}
}

func TestRestrictModeRejectsSystemTempDirectory(t *testing.T) {
	info := fakeDirInfo(os.ModeDir | 0o777)
	err := restrictModeWith("/tmp", privateDirPerm, false, func(string) (os.FileInfo, error) {
		return info, nil
	}, func(string, os.FileMode) error {
		t.Fatal("must not chmod /tmp")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "sticky shared directory") {
		t.Fatalf("expected system temp directory error, got %v", err)
	}
}

func TestRestrictModeRejectsSystemTempDirectoryAlreadyPrivate(t *testing.T) {
	info := fakeDirInfo(os.ModeDir | 0o700)
	err := restrictModeWith("/tmp", privateDirPerm, false, func(string) (os.FileInfo, error) {
		return info, nil
	}, func(string, os.FileMode) error {
		t.Fatal("must not chmod /tmp even when it reports 0700")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "sticky shared directory") {
		t.Fatalf("expected /tmp at 0700 to be refused, got %v", err)
	}
}

func TestEnsurePrivateDirRepairsWorldWritableDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o0777); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dir, 0o700)
}

func TestEnsurePrivateDirRejectsStickyDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tmp")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o1777); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSticky == 0 {
		t.Skip("filesystem does not preserve sticky bit")
	}
	if err := EnsurePrivateDir(dir); err == nil || !strings.Contains(err.Error(), "sticky shared directory") {
		t.Fatalf("expected sticky shared directory error, got %v", err)
	}
	assertMode(t, dir, 0o777)
}

type fakeDirInfo os.FileMode

func (f fakeDirInfo) Name() string       { return "dir" }
func (f fakeDirInfo) Size() int64        { return 0 }
func (f fakeDirInfo) Mode() os.FileMode  { return os.FileMode(f) }
func (f fakeDirInfo) ModTime() time.Time { return time.Time{} }
func (f fakeDirInfo) IsDir() bool        { return true }
func (f fakeDirInfo) Sys() any           { return nil }

func TestEnsurePrivateDirMkdirFailure(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(filepath.Join(blocker, "config")); err == nil {
		t.Fatal("expected mkdir failure")
	}
}

func TestRestrictModePostStatError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.db")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := restrictModeWith(path, privateFilePerm, false, func(name string) (os.FileInfo, error) {
		calls++
		if calls == 1 {
			return os.Stat(name)
		}
		return nil, fmt.Errorf("stat failed")
	}, func(string, os.FileMode) error {
		return nil
	})
	if err == nil || err.Error() != "stat failed" {
		t.Fatalf("expected post-stat error, got %v", err)
	}
}
