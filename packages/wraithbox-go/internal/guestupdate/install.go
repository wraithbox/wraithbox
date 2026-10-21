package guestupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/version"
)

// program is a program of the disk, open for reading.
type program struct {
	name string
	f    *os.File
	size int64
}

// install reads the disk's VERSION and, when it isn't cfg.Own, checks
// every program on the disk before it installs any, then installs them
// in cfg.Programs' order. It returns whether it installed them, and the
// disk's version once it has read a valid one.
func install(ctx context.Context, cfg Config, disk *os.Root) (installed bool, diskVersion string, err error) {
	diskVersion, err = readVersion(disk)
	if err != nil {
		return false, "", err
	}
	if diskVersion == cfg.Own {
		cfg.Log.LogAttrs(ctx, slog.LevelInfo, "tools disk is current", slog.String("version", version.Label(cfg.Own)))
		return false, diskVersion, nil
	}
	progs, err := openPrograms(disk, cfg.Programs, cfg.MaxSize)
	defer func() {
		for _, p := range progs {
			_ = p.f.Close()
		}
	}()
	if err != nil {
		return false, diskVersion, err
	}
	dir, err := os.OpenRoot(cfg.Dir)
	if err != nil {
		return false, diskVersion, &Refusal{Rule: RuleInstallFailed, err: err}
	}
	defer dir.Close()
	if self := progs[len(progs)-1]; sameBytes(dir, self) {
		return false, diskVersion, &Refusal{Rule: RuleNoEffect, err: errors.New(
			"the installed " + SelfName + " is the disk's, but its version isn't the disk's VERSION")}
	}
	for _, p := range progs {
		if err := installOne(dir, p); err != nil {
			return false, diskVersion, &Refusal{Rule: RuleInstallFailed, err: err}
		}
	}
	cfg.Log.LogAttrs(ctx, slog.LevelInfo, "update installed",
		slog.String("from", version.Label(cfg.Own)), slog.String("to", version.Label(diskVersion)),
		slog.Any("programs", cfg.Programs), slog.String("dir", cfg.Dir))
	return true, diskVersion, nil
}

// readVersion reads the disk's VERSION: a regular file that holds a
// build version and nothing else.
func readVersion(disk *os.Root) (string, error) {
	f, _, err := openRegular(disk, appbundle.ToolsVersionFile, version.MaxLen)
	if err != nil {
		return "", &Refusal{Rule: RuleVersionInvalid, err: err}
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, version.MaxLen+1))
	if err != nil {
		return "", &Refusal{Rule: RuleVersionInvalid, err: err}
	}
	if v := string(b); version.Valid(v) {
		return v, nil
	}
	return "", &Refusal{Rule: RuleVersionInvalid, err: fmt.Errorf("%s holds %s, not a build version",
		appbundle.ToolsVersionFile, version.Label(string(b)))}
}

// openPrograms opens each program of names on the disk. It requires
// SelfName last, so wb-guestd is installed after the others. It returns
// the programs it opened, also on an error, for the caller to close.
func openPrograms(disk *os.Root, names []string, maxSize int64) ([]program, error) {
	if len(names) == 0 || names[len(names)-1] != SelfName {
		return nil, &Refusal{Rule: RuleProgramInvalid, err: fmt.Errorf("the programs %q don't end in %s", names, SelfName)}
	}
	progs := make([]program, 0, len(names))
	for _, name := range names {
		f, fi, err := openRegular(disk, name, maxSize)
		if err != nil {
			return progs, &Refusal{Rule: RuleProgramInvalid, err: err}
		}
		progs = append(progs, program{name: name, f: f, size: fi.Size()})
	}
	return progs, nil
}

// openRegular opens name in root when it is a regular file of at most
// maxSize bytes. A symbolic link is refused, not followed.
func openRegular(root *os.Root, name string, maxSize int64) (*os.File, fs.FileInfo, error) {
	fi, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s: not a regular file: %v", name, fi.Mode().Type())
	}
	if fi.Size() > maxSize {
		return nil, nil, fmt.Errorf("%s: %d bytes, over the cap of %d", name, fi.Size(), maxSize)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err == nil && !os.SameFile(fi, st) {
		err = fmt.Errorf("%s: changed while it was opened", name)
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, st, nil
}

// sameBytes reports whether p's file in dir holds the same bytes as p.
func sameBytes(dir *os.Root, p program) bool {
	f, fi, err := openRegular(dir, p.name, p.size)
	if err != nil {
		return false
	}
	defer f.Close()
	if fi.Size() != p.size {
		return false
	}
	if _, err := p.f.Seek(0, io.SeekStart); err != nil {
		return false
	}
	a, b := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		na, errA := io.ReadFull(p.f, a)
		nb, errB := io.ReadFull(f, b)
		if na != nb || !bytes.Equal(a[:na], b[:nb]) {
			return false
		}
		if errA != nil || errB != nil {
			return atEnd(errA) && atEnd(errB)
		}
	}
}

// atEnd reports whether err from io.ReadFull means the reader ended.
func atEnd(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// installOne copies p into dir under a temporary name, created
// exclusively, makes it mode 0755 and syncs it, then renames it over
// p.name. A link at either name is replaced, not followed. The file
// belongs to wb-guestd's user, root. A crash before the rename leaves
// the old program, which the next start replaces.
func installOne(dir *os.Root, p program) error {
	tmp := "." + p.name + ".new"
	if err := dir.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if _, err := p.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return err
	}
	err = writeProgram(f, p)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = dir.Rename(tmp, p.name)
	}
	if err != nil {
		_ = dir.Remove(tmp)
	}
	return err
}

func writeProgram(f *os.File, p program) error {
	n, err := io.Copy(f, io.LimitReader(p.f, p.size+1))
	if err != nil {
		return err
	}
	if n != p.size {
		return fmt.Errorf("%s: read %d bytes, want %d", p.name, n, p.size)
	}
	if err := f.Chmod(0o755); err != nil {
		return err
	}
	return f.Sync()
}
