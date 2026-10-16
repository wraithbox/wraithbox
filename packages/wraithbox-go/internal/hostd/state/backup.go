package state

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
)

// keepBackups is how many backups stay: the two newest
// (S17-05-upgrades-and-migrations, "The backup").
const keepBackups = 2

// backupName matches state.db.v<N>.bak, the only names pruning deletes.
var backupName = regexp.MustCompile(`^state\.db\.v([0-9]{1,9})\.bak$`)

// tmpPattern is the os.CreateTemp pattern of a backup being written,
// and the glob of the files a start that stopped half way left. The next
// backup deletes those.
const tmpPattern = "state.db.bak.tmp-*"

// BackupName returns the backup file name of a database at version.
func BackupName(version int) string {
	return fmt.Sprintf("%s.v%d.bak", FileName, version)
}

// writeBackup copies the database at version to state.db.v<version>.bak
// in dataDir, and returns its path (S17-05-upgrades-and-migrations, "The
// backup"):
//
//  1. When that name exists, the copy from an earlier attempt stays and
//     nothing is written.
//  2. VACUUM INTO writes a copy to a new temporary name, created
//     exclusively with mode 600.
//  3. The copy's own version is read again. A second wb-hostd can
//     migrate between this one's version read and the copy, so the copy
//     is named after the version it holds, and a name never holds a
//     later schema.
//  4. The copy is linked to the backup name. A link fails on an existing
//     name, so it never writes over a backup.
//
// Then only the two newest backups are kept.
func writeBackup(ctx context.Context, db *sql.DB, dataDir string, version int,
	vacuum func(context.Context, *sql.DB, string) error,
) (string, *Refusal) {
	final := filepath.Join(dataDir, BackupName(version))
	removeLeftovers(ctx, dataDir)
	kept, err := exists(final)
	if err != nil {
		return "", backupFailed(dataDir, version, err)
	}
	if kept {
		slog.Default().LogAttrs(ctx, slog.LevelInfo, "state database backup kept from an earlier attempt",
			slog.String("backup", final), slog.Int("version", version))
	} else if final, err = copyTo(ctx, db, dataDir, version, vacuum); err != nil {
		return "", backupFailed(dataDir, version, err)
	}
	prune(ctx, dataDir)
	return final, nil
}

// copyTo writes the copy and returns the backup's path, named after
// the version the copy holds.
func copyTo(ctx context.Context, db *sql.DB, dataDir string, version int,
	vacuum func(context.Context, *sql.DB, string) error,
) (string, error) {
	// CreateTemp opens with O_EXCL and mode 600. VACUUM INTO accepts an
	// empty file, and keeps its mode.
	f, err := os.CreateTemp(dataDir, tmpPattern)
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := vacuum(ctx, db, tmp); err != nil {
		return "", fmt.Errorf("VACUUM INTO: %w", err)
	}
	if err := syncFile(tmp); err != nil {
		return "", err
	}
	copied, err := fileVersion(ctx, tmp)
	if err != nil {
		return "", fmt.Errorf("reading the copy's version: %w", err)
	}
	final := filepath.Join(dataDir, BackupName(copied))
	if copied != version {
		slog.Default().LogAttrs(ctx, slog.LevelWarn, "state database changed during the backup",
			slog.Int("version", version), slog.Int("copied", copied), slog.String("backup", final))
		kept, err := exists(final)
		if err != nil || kept {
			return final, err
		}
	}
	if err := os.Link(tmp, final); err != nil {
		if errors.Is(err, fs.ErrExist) {
			slog.Default().LogAttrs(ctx, slog.LevelInfo, "state database backup kept from an earlier attempt",
				slog.String("backup", final))
			return final, nil
		}
		return "", err
	}
	slog.Default().LogAttrs(ctx, slog.LevelInfo, "state database backup written", slog.String("backup", final))
	return final, nil
}

// fileVersion reads the user_version of the database at path, which
// must be 0 or more.
func fileVersion(ctx context.Context, path string) (int, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, err
	}
	if v < 0 {
		return 0, fmt.Errorf("user_version is %d, below 0", v)
	}
	return v, nil
}

// vacuumInto is the backup copy of S17-05-upgrades-and-migrations, "The
// backup".
func vacuumInto(ctx context.Context, db *sql.DB, dst string) error {
	_, err := db.ExecContext(ctx, "VACUUM INTO ?", dst)
	return err
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// exists reports whether a backup is at path. Only a regular file
// counts as one. Anything else at the name, such as a directory or a
// symbolic link, is an error: it would stand in for a backup that
// doesn't exist.
func exists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file (%s)", path, info.Mode().Type())
	}
	return true, nil
}

func backupFailed(dataDir string, version int, err error) *Refusal {
	return &Refusal{
		Rule:    RuleBackupFailed,
		Version: version,
		Backup:  newestBackup(dataDir),
		err:     err,
		msg: fmt.Sprintf("the backup of the state database at version %d couldn't be written to %s: %v; no migration ran",
			version, dataDir, err),
	}
}

// removeLeftovers deletes temporary copies a start that stopped half way
// left behind. A failure is logged and doesn't stop the backup.
func removeLeftovers(ctx context.Context, dataDir string) {
	// The pattern is matched against names only, so a glob character in
	// dataDir means nothing.
	entries, _ := os.ReadDir(dataDir)
	for _, e := range entries {
		if ok, _ := filepath.Match(tmpPattern, e.Name()); !ok {
			continue
		}
		m := filepath.Join(dataDir, e.Name())
		if err := os.Remove(m); err != nil {
			slog.Default().LogAttrs(ctx, slog.LevelWarn, "state database leftover copy not removed",
				slog.String("path", m), slog.String("reason", err.Error()))
		}
	}
}

type backupFile struct {
	name    string
	version int
}

// backups lists the backups in dataDir, newest first. Newest is by the
// version in the name, which only grows.
func backups(dataDir string) []backupFile {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}
	var out []backupFile
	for _, e := range entries {
		m := backupName.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		out = append(out, backupFile{name: e.Name(), version: v})
	}
	slices.SortFunc(out, func(a, b backupFile) int { return cmp.Compare(b.version, a.version) })
	return out
}

// newestBackup returns the path of the newest backup, or "".
func newestBackup(dataDir string) string {
	b := backups(dataDir)
	if len(b) == 0 {
		return ""
	}
	return filepath.Join(dataDir, b[0].name)
}

// prune deletes all but the keepBackups newest backups. A failure is
// logged and doesn't stop the migration: the backup it needs exists.
func prune(ctx context.Context, dataDir string) {
	b := backups(dataDir)
	if len(b) <= keepBackups {
		return
	}
	for _, old := range b[keepBackups:] {
		path := filepath.Join(dataDir, old.name)
		if err := os.Remove(path); err != nil {
			slog.Default().LogAttrs(ctx, slog.LevelWarn, "old state database backup not removed",
				slog.String("path", path), slog.String("reason", err.Error()))
			continue
		}
		slog.Default().LogAttrs(ctx, slog.LevelInfo, "old state database backup removed", slog.String("path", path))
	}
}
