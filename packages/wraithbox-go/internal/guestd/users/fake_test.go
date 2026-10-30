package users_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/procs"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// fakeDir is an in-memory local directory. Like macOS, it refuses a
// change of GeneratedUID or NFSHomeDirectory once a user record has an
// ID (X08-data-disk, answer 11).
type fakeDir struct {
	recs map[gu.Kind]map[string]gu.Record
	// writes is every Create, as "kind name attr=value", in order.
	writes []string
	reads  int
	// listErr, readErr, createErr and disableErr fail every call of
	// their kind.
	listErr, readErr, createErr, disableErr error
	// drop makes Create succeed without writing attribute drop.
	drop string
}

func newFakeDir() *fakeDir {
	return &fakeDir{recs: map[gu.Kind]map[string]gu.Record{gu.Users: {}, gu.Groups: {}}}
}

// put sets a whole record as guest root might have left it.
func (d *fakeDir) put(kind gu.Kind, name string, rec gu.Record) {
	if _, ok := rec[gu.AttrRecordName]; !ok {
		rec[gu.AttrRecordName] = []string{name}
	}
	d.recs[kind][name] = rec
}

func (d *fakeDir) List(_ context.Context, kind gu.Kind, attrs []string) ([]gu.Record, error) {
	if d.listErr != nil {
		return nil, d.listErr
	}
	names := slices.Sorted(maps.Keys(d.recs[kind]))
	out := make([]gu.Record, 0, len(names))
	for _, n := range names {
		rec := gu.Record{}
		for _, a := range attrs {
			if v, ok := d.recs[kind][n][a]; ok {
				rec[a] = slices.Clone(v)
			}
		}
		out = append(out, rec)
	}
	return out, nil
}

func (d *fakeDir) Read(_ context.Context, kind gu.Kind, name string) (gu.Record, bool, error) {
	d.reads++
	if d.readErr != nil {
		return nil, false, d.readErr
	}
	for _, rec := range d.recs[kind] {
		if slices.Contains(rec[gu.AttrRecordName], name) {
			return cloneRec(rec), true, nil
		}
	}
	return nil, false, nil
}

func (d *fakeDir) Create(_ context.Context, kind gu.Kind, name, attr, value string) error {
	d.writes = append(d.writes, fmt.Sprintf("%s %s %s=%s", kind, name, attr, value))
	if d.createErr != nil {
		return d.createErr
	}
	rec, ok := d.recs[kind][name]
	if !ok {
		rec = gu.Record{gu.AttrRecordName: {name}}
		d.recs[kind][name] = rec
	}
	locked := kind == gu.Users && len(rec[gu.AttrUniqueID]) > 0
	if locked && (attr == gu.AttrGeneratedUID || attr == gu.AttrHome) {
		return errors.New("fake: eDSPermissionError")
	}
	// Like dscl, it keeps an attribute without a standard name under
	// its native name.
	if attr != d.drop {
		rec[gu.RecordKey(attr)] = []string{value}
	}
	return nil
}

// Disable records the call as "disable name" in writes.
func (d *fakeDir) Disable(_ context.Context, name string) error {
	d.writes = append(d.writes, "disable "+name)
	return d.disableErr
}

func cloneRec(rec gu.Record) gu.Record {
	out := gu.Record{}
	for k, v := range rec {
		out[k] = slices.Clone(v)
	}
	return out
}

// fakeRoot is an in-memory volume root.
type fakeRoot struct {
	entries map[string]gu.Entry
	made    []string
	// removed is every name Rmdir removed, and full each name it fails
	// on, as a home with entries the user couldn't remove.
	removed []string
	full    map[string]bool
	// openErr fails the open, and entriesErr every Entries.
	openErr, entriesErr error
	opens, closes       int
}

func newFakeRoot() *fakeRoot { return &fakeRoot{entries: map[string]gu.Entry{}} }

func (r *fakeRoot) open() (gu.Root, error) {
	r.opens++
	if r.openErr != nil {
		return nil, r.openErr
	}
	return r, nil
}

func (r *fakeRoot) Stat(name string) (gu.Entry, error) {
	e, ok := r.entries[name]
	if !ok {
		return gu.Entry{}, fmt.Errorf("fake: %s: %w", name, fs.ErrNotExist)
	}
	return e, nil
}

func (r *fakeRoot) Mkdir(name string, uid, gid uint32) error {
	if _, ok := r.entries[name]; ok {
		return fmt.Errorf("fake: %s: %w", name, fs.ErrExist)
	}
	r.made = append(r.made, name)
	r.entries[name] = gu.Entry{Name: name, IsDir: true, UID: uid, GID: gid, Perm: 0o700}
	return nil
}

func (r *fakeRoot) Entries(limit int) ([]gu.Entry, error) {
	if r.entriesErr != nil {
		return nil, r.entriesErr
	}
	if len(r.entries) > limit {
		return nil, gu.ErrTooManyEntries
	}
	out := slices.Collect(maps.Values(r.entries))
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r *fakeRoot) Rmdir(name string) error {
	e, ok := r.entries[name]
	switch {
	case !ok:
		return fmt.Errorf("fake: %s: %w", name, fs.ErrNotExist)
	case !e.IsDir || e.IsLink:
		return fmt.Errorf("fake: %s: not a directory", name)
	case r.full[name]:
		return fmt.Errorf("fake: %s: directory not empty", name)
	}
	delete(r.entries, name)
	r.removed = append(r.removed, name)
	return nil
}

func (*fakeRoot) OpenDir(string) (gu.Dir, error) { return nil, errors.New("fake: no OpenDir") }

func (r *fakeRoot) Mount() (guestmount.Mount, error) { return guestmount.Mount{}, nil }

func (r *fakeRoot) Close() error { r.closes++; return nil }

// rootOK is a RootCheck that passes every root.
func rootOK(context.Context, gu.Root) (rule, found string) { return "", "" }

// fakeKiller is the kill loop: it reports left for each kill, and
// records each call in calls.
type fakeKiller struct {
	left  uint32
	calls *[]string
}

func (k *fakeKiller) Kill(_ context.Context, uid uint32) procs.KillResult {
	*k.calls = append(*k.calls, fmt.Sprintf("kill %d", uid))
	return procs.KillResult{Left: k.left, Rounds: 1}
}

// fakeEmptier is the child that empties a home: it records its IDs and
// home in calls and its deadline in deadline, runs onRun when set, and
// returns err.
type fakeEmptier struct {
	err      error
	calls    *[]string
	deadline time.Time
	onRun    func()
}

func (e *fakeEmptier) EmptyHome(ctx context.Context, uid, gid uint32, home string) error {
	*e.calls = append(*e.calls, fmt.Sprintf("empty %d:%d %s", uid, gid, home))
	e.deadline, _ = ctx.Deadline()
	if e.onRun != nil {
		e.onRun()
	}
	return e.err
}

// exitError is a child that ran and exited nonzero.
type exitError struct{}

func (exitError) Error() string { return "exit status 1" }

func (exitError) ExitCode() int { return 1 }
