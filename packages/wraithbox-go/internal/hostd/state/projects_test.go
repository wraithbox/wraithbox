package state

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

const (
	idA = "0123456789abcdef0123456789abcdef"
	idB = "fedcba9876543210fedcba9876543210"
)

func newProjectsDB(t *testing.T) *DB {
	t.Helper()
	return mustOpen(t, t.TempDir(), opts(migrations))
}

func projectRow(id, path string) Project {
	return Project{
		ID: id, Realm: "native", KeyPath: path, Name: "foo", Placement: PlacementWork,
		URLs: []string{"https://a.example/a.git"}, Created: time.Unix(100, 0).UTC(),
	}
}

func inTx(t *testing.T, db *DB, fn func(*ProjectTx) error) error {
	t.Helper()
	return db.Projects(t.Context(), fn)
}

// TestProjectsMigrationFromLast: the projects migration runs on a
// database at the version before it, and keeps that version's rows.
func TestProjectsMigrationFromLast(t *testing.T) {
	dir := t.TempDir()
	last := len(migrations) - 1
	if migrations[last].name != "projects" {
		t.Fatalf("the last migration is %s, want projects", migrations[last].name)
	}
	db := mustOpen(t, dir, opts(migrations[:last]))
	must(t, db.RecordDataDiskFormat(t.Context(), workDisk))
	_ = db.Close()

	db = mustOpen(t, dir, opts(migrations))
	if v := version(t, db.Path()); v != len(migrations) {
		t.Errorf("version = %d, want %d", v, len(migrations))
	}
	if !hasTable(t, db.Path(), "projects") {
		t.Error("projects missing")
	}
	if n := count(t, db.Path(), "data_disks"); n != 1 {
		t.Errorf("data_disks rows = %d, want 1", n)
	}
}

// seedTwo inserts a, with trust and a root commit, and b, older and
// without URLs, and returns them.
func seedTwo(t *testing.T, db *DB) (a, b Project) {
	t.Helper()
	a = projectRow(idA, "/Users/a/foo/.git")
	a.ConfigTrust = true
	a.RootCommits = []string{"1111111111111111111111111111111111111111"}
	b = projectRow(idB, "/Users/a/bar/.git")
	b.URLs = nil
	b.Created = time.Unix(50, 0).UTC()
	must(t, inTx(t, db, func(p *ProjectTx) error {
		if err := p.Insert(a); err != nil {
			return err
		}
		return p.Insert(b)
	}))
	return a, b
}

// TestProjectLists: All and ByName return every match, oldest first.
func TestProjectLists(t *testing.T) {
	db := newProjectsDB(t)
	seedTwo(t, db)
	must(t, inTx(t, db, func(p *ProjectTx) error {
		all, err := p.All()
		if err != nil || len(all) != 2 || all[0].ID != idB {
			t.Errorf("All = %+v (%v), want b then a", all, err)
		}
		named, err := p.ByName("foo")
		if err != nil || len(named) != 2 || named[0].ID != idB {
			t.Errorf("ByName = %+v (%v), want b then a", named, err)
		}
		return nil
	}))
}

// TestProjectRoundTrip: what Insert writes, ByID and ByKey read back,
// and an unknown id isn't found.
func TestProjectRoundTrip(t *testing.T) {
	db := newProjectsDB(t)
	a, b := seedTwo(t, db)
	must(t, inTx(t, db, func(p *ProjectTx) error {
		got, ok, err := p.ByID(idA)
		if err != nil || !ok || !equalProject(got, a) {
			t.Errorf("ByID = %+v (%t, %v), want %+v", got, ok, err, a)
		}
		got, ok, err = p.ByKey("native", b.KeyPath)
		if err != nil || !ok || !equalProject(got, b) {
			t.Errorf("ByKey = %+v (%t, %v), want %+v", got, ok, err, b)
		}
		_, ok, err = p.ByID(idA[:31] + "0")
		if err != nil || ok {
			t.Errorf("ByID of an unknown id = (%t, %v)", ok, err)
		}
		return nil
	}))
}

func equalProject(a, b Project) bool {
	return a.ID == b.ID && a.Realm == b.Realm && a.KeyPath == b.KeyPath && a.Name == b.Name &&
		a.Placement == b.Placement && a.ConfigTrust == b.ConfigTrust && a.Created.Equal(b.Created) &&
		slices.Equal(a.URLs, b.URLs) && slices.Equal(a.RootCommits, b.RootCommits)
}

// TestProjectRefusals: a second project with the same key, an id
// another project holds, a move onto a taken key, and rows outside the
// table's checks are refused, and the transaction leaves nothing.
func TestProjectRefusals(t *testing.T) {
	db := newProjectsDB(t)
	a := projectRow(idA, "/Users/a/foo/.git")
	must(t, inTx(t, db, func(p *ProjectTx) error { return p.Insert(a) }))
	b := projectRow(idB, "/Users/a/bar/.git")
	must(t, inTx(t, db, func(p *ProjectTx) error { return p.Insert(b) }))

	wantErr(t, "same key", inTx(t, db, func(p *ProjectTx) error {
		return p.Insert(projectRow("00000000000000000000000000000000", a.KeyPath))
	}), ErrProjectKeyTaken)
	wantErr(t, "same id", inTx(t, db, func(p *ProjectTx) error {
		return p.Insert(projectRow(idA, "/Users/a/baz/.git"))
	}), ErrProjectIDTaken)
	moved := b
	moved.KeyPath = a.KeyPath
	wantErr(t, "move onto a taken key", inTx(t, db, func(p *ProjectTx) error { return p.Update(moved) }), ErrProjectKeyTaken)
	wantErr(t, "update of an unknown id", inTx(t, db, func(p *ProjectTx) error {
		return p.Update(projectRow("11111111111111111111111111111111", "/x/.git"))
	}), ErrProjectNotFound)
	wantErr(t, "delete of an unknown id", inTx(t, db, func(p *ProjectTx) error {
		return p.Delete("11111111111111111111111111111111")
	}), ErrProjectNotFound)

	bad := []struct {
		name string
		edit func(*Project)
	}{
		{"upper-case id", func(r *Project) { r.ID = "0123456789ABCDEF0123456789ABCDEF" }},
		{"short id", func(r *Project) { r.ID = "0123" }},
		{"other realm", func(r *Project) { r.Realm = "wsl" }},
		{"no placement", func(r *Project) { r.Placement = "" }},
		{"empty name", func(r *Project) { r.Name = "" }},
	}
	for _, tt := range bad {
		row := projectRow("22222222222222222222222222222222", "/Users/a/new/.git")
		tt.edit(&row)
		err := inTx(t, db, func(p *ProjectTx) error { return p.Insert(row) })
		if err == nil {
			t.Errorf("%s: inserted", tt.name)
		}
	}
	if n := count(t, db.Path(), "projects"); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
}

// TestProjectUpdateAndDelete: an update keeps the id and created time,
// and a delete removes the row. A failing fn rolls back.
func TestProjectUpdateAndDelete(t *testing.T) {
	db := newProjectsDB(t)
	a := projectRow(idA, "/Users/a/foo/.git")
	must(t, inTx(t, db, func(p *ProjectTx) error { return p.Insert(a) }))
	moved := a
	moved.KeyPath = "/Users/a/moved/.git"
	moved.Name = "moved"
	moved.Created = time.Unix(999, 0)
	must(t, inTx(t, db, func(p *ProjectTx) error { return p.Update(moved) }))
	errStop := errors.New("stop")
	wantErr(t, "rolled back", inTx(t, db, func(p *ProjectTx) error {
		if err := p.Delete(idA); err != nil {
			return err
		}
		return errStop
	}), errStop)
	must(t, inTx(t, db, func(p *ProjectTx) error {
		got, ok, err := p.ByID(idA)
		if err != nil || !ok || got.KeyPath != moved.KeyPath || got.Name != "moved" || !got.Created.Equal(a.Created) {
			t.Errorf("after the move = %+v (%t, %v)", got, ok, err)
		}
		return p.Delete(idA)
	}))
	if n := count(t, db.Path(), "projects"); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

// TestProjectInsertRace: of many transactions inserting one key at
// once, each with its own id, exactly one succeeds, and every other is
// refused with ErrProjectKeyTaken (S05-04-naming, "Project id": a key
// has at most one project).
func TestProjectInsertRace(t *testing.T) {
	db := newProjectsDB(t)
	const n = 16
	errs := make(chan error, n)
	for i := range n {
		go func() {
			row := projectRow(fmt.Sprintf("%032x", i+1), "/Users/a/foo/.git")
			errs <- db.Projects(t.Context(), func(p *ProjectTx) error { return p.Insert(row) })
		}()
	}
	won := 0
	for range n {
		switch err := <-errs; {
		case err == nil:
			won++
		case !errors.Is(err, ErrProjectKeyTaken):
			t.Errorf("err = %v, want ErrProjectKeyTaken", err)
		}
	}
	if won != 1 {
		t.Errorf("%d inserts succeeded, want 1", won)
	}
	if n := count(t, db.Path(), "projects"); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}
