//go:build unix

package state

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Windows has no mode bits, so the mode checks run on Unix hosts only.

func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// C12-install-and-downloads, "Migrations": the database and each backup
// are mode 600, whatever the umask, and a database of another mode is
// set to 600.
func TestC12ModeOf600(t *testing.T) {
	t.Run("new database", func(t *testing.T) {
		dir := t.TempDir()
		mustOpen(t, dir, opts(migrations))
		if m := mode(t, filepath.Join(dir, FileName)); m != 0o600 {
			t.Errorf("database mode = %v, want 0600", m)
		}
	})
	t.Run("existing database and backup", func(t *testing.T) {
		dir := t.TempDir()
		path := fixture(t, dir, 0, "CREATE TABLE legacy (x)")
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		mustOpen(t, dir, opts(migrations))
		if m := mode(t, path); m != 0o600 {
			t.Errorf("database mode = %v, want 0600", m)
		}
		if m := mode(t, filepath.Join(dir, BackupName(0))); m != 0o600 {
			t.Errorf("backup mode = %v, want 0600", m)
		}
	})
}

// A symbolic link at the database's name is refused, not followed.
func TestSymlinkRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere.db")
	fixture(t, filepath.Dir(target), 0)
	if err := os.Rename(filepath.Join(filepath.Dir(target), FileName), target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	_, err := Open(t.Context(), dir)
	refusal(t, err, RuleOpenFailed)
}

// A data directory the backup can't be written in refuses with
// state-db-backup-failed, through the real VACUUM INTO.
func TestBackupDirectoryNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes in a read-only directory")
	}
	dir := t.TempDir()
	path := fixture(t, dir, 0, "CREATE TABLE legacy (x)")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err := Open(t.Context(), dir)
	refusal(t, err, RuleBackupFailed)
	if v := version(t, path); v != 0 {
		t.Errorf("version = %d after the failed backup, want 0", v)
	}
}
