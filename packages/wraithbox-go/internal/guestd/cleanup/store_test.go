package cleanup_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/cleanup"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// memFile is a file of memDir.
type memFile struct {
	data  []byte
	entry gu.Entry
}

// memDir is an in-memory Dir, and memRoot a Root that holds one.
type memDir struct {
	self     gu.Entry
	files    map[string]memFile
	writeErr error
}

func (d *memDir) Self() (gu.Entry, error) { return d.self, nil }

func (d *memDir) Names(limit int) ([]string, error) {
	if len(d.files) > limit {
		return nil, gu.ErrTooManyEntries
	}
	return slices.Sorted(maps.Keys(d.files)), nil
}

func (d *memDir) ReadFile(name string, limit int) ([]byte, gu.Entry, error) {
	f, ok := d.files[name]
	switch {
	case !ok:
		return nil, gu.Entry{}, fs.ErrNotExist
	case f.entry.IsLink:
		return nil, f.entry, errors.New("too many levels of symbolic links")
	case f.entry.IsDir:
		return nil, f.entry, gu.ErrNotRegular
	case len(f.data) > limit:
		return nil, f.entry, gu.ErrTooBig
	}
	return f.data, f.entry, nil
}

func (d *memDir) WriteFile(name string, data []byte) error {
	if d.writeErr != nil {
		return d.writeErr
	}
	d.files[name] = memFile{data: data, entry: gu.Entry{Name: name, Perm: 0o600}}
	return nil
}

func (*memDir) Close() error { return nil }

type memRoot struct {
	dir      *memDir
	mkdirErr error
	openErr  error
}

func (*memRoot) Stat(string) (gu.Entry, error) { return gu.Entry{}, fs.ErrNotExist }

func (r *memRoot) Mkdir(string, uint32, uint32) error {
	if r.mkdirErr != nil {
		return r.mkdirErr
	}
	if r.dir != nil {
		return fs.ErrExist
	}
	r.dir = &memDir{self: gu.Entry{Name: ".", IsDir: true, Perm: 0o700}, files: map[string]memFile{}}
	return nil
}

func (*memRoot) Entries(int) ([]gu.Entry, error) { return nil, nil }

func (*memRoot) Rmdir(string) error { return nil }

func (r *memRoot) OpenDir(string) (gu.Dir, error) {
	if r.openErr != nil {
		return nil, r.openErr
	}
	return r.dir, nil
}

func (*memRoot) Mount() (guestmount.Mount, error) { return guestmount.Mount{}, nil }

func (*memRoot) Close() error { return nil }

func newLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func TestStoreWriteList(t *testing.T) {
	root := &memRoot{}
	log, buf := newLog()
	s, err := cleanup.Open(t.Context(), root, 0, log)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []cleanup.Record{
		{KillID: 1, ProjectID: project, Left: 2},
		{KillID: 2, ProjectID: project},
	} {
		if err := s.Write(t.Context(), 10000, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Write(t.Context(), 10001, cleanup.Record{KillID: 3, ProjectID: project, Left: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(t.Context())
	want := []cleanup.Entry{
		{UID: 10000, Record: cleanup.Record{KillID: 2, ProjectID: project}},
		{UID: 10001, Record: cleanup.Record{KillID: 3, ProjectID: project, Left: 1}},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("List = %+v, %v, want %+v", got, err, want)
	}
	if !strings.Contains(buf.String(), "cleanup record written") {
		t.Errorf("log %s", buf)
	}
	// Opened again, the directory is kept.
	if _, err := cleanup.Open(t.Context(), root, 0, log); err != nil {
		t.Errorf("second Open: %v", err)
	}
}

// TestStoreListSkips: each file that isn't a record is logged with
// RuleFile and skipped.
func TestStoreListSkips(t *testing.T) {
	root := &memRoot{}
	log, buf := newLog()
	s, err := cleanup.Open(t.Context(), root, 0, log)
	if err != nil {
		t.Fatal(err)
	}
	good, _ := cleanup.Format(cleanup.Record{KillID: 7, ProjectID: project})
	root.dir.files = map[string]memFile{
		"10000":     {data: good},
		"10001":     {data: good, entry: gu.Entry{UID: 501}},
		"10002":     {data: []byte("garbage")},
		"10003":     {entry: gu.Entry{IsLink: true}},
		"10004":     {entry: gu.Entry{IsDir: true}},
		"10005":     {data: bytes.Repeat([]byte("x"), cleanup.MaxSize+1)},
		"9999":      {data: good},
		"x\x1b[31m": {data: good},
		"10006.tmp": {data: []byte("half")},
	}
	got, err := s.List(t.Context())
	if err != nil || len(got) != 1 || got[0].UID != 10000 {
		t.Errorf("List = %+v, %v", got, err)
	}
	out := buf.String()
	if n := strings.Count(out, cleanup.RuleFile); n != 7 {
		t.Errorf("%d files refused, want 7: %s", n, out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("an unescaped name in the log: %s", out)
	}
}

// TestStoreOpenRefuses: Open fails closed, with RuleDir in the log,
// when the directory can't be made or opened or isn't root's with mode
// 700.
func TestStoreOpenRefuses(t *testing.T) {
	cases := map[string]func(r *memRoot){
		"mkdir":   func(r *memRoot) { r.mkdirErr = errors.New("read-only") },
		"open":    func(r *memRoot) { r.openErr = errors.New("too many levels of symbolic links") },
		"owner":   func(r *memRoot) { r.dir.self.UID = 501 },
		"mode":    func(r *memRoot) { r.dir.self.Perm = 0o755 },
		"special": func(r *memRoot) { r.dir.self.Special = true },
		"file":    func(r *memRoot) { r.dir.self.IsDir = false },
	}
	for name, change := range cases {
		root := &memRoot{}
		_ = root.Mkdir(cleanup.DirName, 0, 0)
		change(root)
		log, buf := newLog()
		if _, err := cleanup.Open(t.Context(), root, 0, log); err == nil {
			t.Errorf("%s: Open succeeded", name)
		}
		if !strings.Contains(buf.String(), cleanup.RuleDir) {
			t.Errorf("%s: log %s", name, buf)
		}
	}
}

func TestStoreWriteFails(t *testing.T) {
	root := &memRoot{}
	log, buf := newLog()
	s, err := cleanup.Open(t.Context(), root, 0, log)
	if err != nil {
		t.Fatal(err)
	}
	root.dir.writeErr = fmt.Errorf("disk full")
	if err := s.Write(t.Context(), 10000, cleanup.Record{KillID: 1, ProjectID: project}); err == nil {
		t.Error("Write succeeded")
	}
	if err := s.Write(t.Context(), 10000, cleanup.Record{ProjectID: project}); err == nil {
		t.Error("Write of kill ID 0 succeeded")
	}
	if strings.Count(buf.String(), cleanup.RuleWrite) != 2 {
		t.Errorf("log %s", buf)
	}
}
