package cleanup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// DirName is the records' directory in the data volume's root. No
// project user's name starts with a dot, so it is no one's home.
const DirName = ".wb-cleanup"

// MaxRecords bounds a listing: one record per project user that a kill
// stopped and wb-hostd hasn't acknowledged. Guest root can fill the
// directory, and past this many entries List fails.
const MaxRecords = 4096

// The rules of the records.
const (
	// RuleDir: the records' directory couldn't be made or opened, or
	// isn't a directory owned by root with mode 700.
	RuleDir = "cleanup-record-dir"
	// RuleFile: a file in the directory isn't a record: a name that
	// isn't a project user's UID, a link, a file other than a regular
	// one, one not owned by root, too big, or one that Parse refuses.
	RuleFile = "cleanup-record-file"
	// RuleWrite: a record couldn't be written.
	RuleWrite = "cleanup-record-write"
)

// Store is the open records' directory.
type Store struct {
	dir   gu.Dir
	owner uint32
	log   *slog.Logger
}

// Open makes the records' directory in root when it is missing, owned
// by owner, root in wb-guestd, with mode 700, and opens it. It refuses a
// directory that isn't owner's with mode 700, or a link
// (S06-10-leftover-processes, "Kill IDs and records";
// SEC08-proj-isolation). The caller has checked that root is on the
// data mount, and closes the Store before it closes root.
func Open(ctx context.Context, root gu.Root, owner uint32, log *slog.Logger) (*Store, error) {
	if err := root.Mkdir(DirName, owner, owner); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, refuseDir(ctx, log, err.Error())
	}
	dir, err := root.OpenDir(DirName)
	if err != nil {
		return nil, refuseDir(ctx, log, err.Error())
	}
	self, err := dir.Self()
	if err == nil && (!self.IsDir || self.IsLink || self.Special || self.UID != owner || self.Perm != 0o700) {
		err = fmt.Errorf("dir=%t uid=%d gid=%d mode=%04o special=%t",
			self.IsDir, self.UID, self.GID, uint32(self.Perm), self.Special)
	}
	if err != nil {
		_ = dir.Close()
		return nil, refuseDir(ctx, log, err.Error())
	}
	return &Store{dir: dir, owner: owner, log: log}, nil
}

// refuseDir logs a refused directory with RuleDir, and returns the
// error.
func refuseDir(ctx context.Context, log *slog.Logger, found string) error {
	log.LogAttrs(ctx, slog.LevelError, "cleanup records' directory refused",
		slog.String("rule", RuleDir), slog.String("decision", "refuse"), slog.String("found", found))
	return fmt.Errorf("%s: %s", RuleDir, found)
}

// Close closes the directory.
func (s *Store) Close() error { return s.dir.Close() }

// Write replaces the record of uid with r, so a user has at most one
// record, the newest.
func (s *Store) Write(ctx context.Context, uid uint32, r Record) error {
	data, err := Format(r)
	if err == nil {
		err = s.dir.WriteFile(FileName(uid), data)
	}
	attrs := []slog.Attr{
		slog.Uint64("uid", uint64(uid)), slog.Uint64("kill_id", r.KillID),
		slog.String("project_id", r.ProjectID), slog.Uint64("left", uint64(r.Left)),
	}
	if err != nil {
		s.log.LogAttrs(ctx, slog.LevelError, "cleanup record not written",
			append(attrs, slog.String("rule", RuleWrite), slog.String("error", err.Error()))...)
		return fmt.Errorf("%s: %w", RuleWrite, err)
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "cleanup record written", attrs...)
	return nil
}

// Entry is one record and the UID its file is named for.
type Entry struct {
	UID    uint32
	Record Record
}

// List reads every record. A file that isn't a record is logged with
// RuleFile and skipped: it never counts as a result. A temporary file
// of a write is skipped without a log.
func (s *Store) List(ctx context.Context) ([]Entry, error) {
	names, err := s.dir.Names(MaxRecords)
	if err != nil {
		return nil, refuseDir(ctx, s.log, err.Error())
	}
	var out []Entry
	for _, name := range names {
		if strings.HasSuffix(name, ".tmp") {
			continue
		}
		e, err := s.read(name)
		if err != nil {
			s.log.LogAttrs(ctx, slog.LevelWarn, "cleanup record refused",
				slog.String("rule", RuleFile), slog.String("decision", "skip"),
				slog.String("file", label(name)), slog.String("found", err.Error()))
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// read reads and parses the file name.
func (s *Store) read(name string) (Entry, error) {
	uid, err := UIDOf(name)
	if err != nil {
		return Entry{}, err
	}
	data, st, err := s.dir.ReadFile(name, MaxSize)
	if err != nil {
		return Entry{}, err
	}
	if st.UID != s.owner {
		return Entry{}, fmt.Errorf("owned by UID %d", st.UID)
	}
	r, err := Parse(data)
	if err != nil {
		return Entry{}, err
	}
	return Entry{UID: uid, Record: r}, nil
}

// label is a file name for a log record, cut and quoted.
func label(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	return fmt.Sprintf("%+q", s)
}
