package state

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

var isolatedDisk = DataDisk{Slot: "macos-isolated", VolumeUUID: uuidB, TempName: "data.wbdisk.tmp", SizeGiB: 128}

// formatDisk records rec as formatted.
func formatDisk(t *testing.T, db *DB, rec DataDisk) {
	t.Helper()
	must(t, db.RecordDataDiskFormat(t.Context(), rec))
	must(t, db.MarkDataDiskFormatted(t.Context(), rec.Slot, rec.VolumeUUID))
}

// newUsersDB is a database with the work and isolated data disks
// formatted, and projects A and B registered.
func newUsersDB(t *testing.T) *DB {
	t.Helper()
	db := newProjectsDB(t)
	formatDisk(t, db, workDisk)
	formatDisk(t, db, isolatedDisk)
	must(t, inTx(t, db, func(tx *ProjectTx) error {
		if err := tx.Insert(projectRow(idA, "/a")); err != nil {
			return err
		}
		return tx.Insert(projectRow(idB, "/b"))
	}))
	return db
}

func nextID(t *testing.T, db *DB) int64 {
	t.Helper()
	var n int64
	must(t, db.db.QueryRowContext(t.Context(), `SELECT next_id FROM project_user_ids`).Scan(&n))
	return n
}

func userOf(t *testing.T, db *DB, project, slot string) ProjectUser {
	t.Helper()
	u, _, err := db.ProjectUser(t.Context(), project, slot)
	must(t, err)
	return u
}

// TestProjectUsersMigration: the counter starts at 10000 on a new
// database, and on one upgraded from the version before, whose rows stay.
func TestProjectUsersMigration(t *testing.T) {
	t.Run("new", func(t *testing.T) {
		if n := nextID(t, newProjectsDB(t)); n != 10000 {
			t.Errorf("next_id = %d, want 10000", n)
		}
	})
	t.Run("upgraded", func(t *testing.T) {
		dir := t.TempDir()
		at := slices.IndexFunc(migrations, func(m migration) bool { return m.name == "project-users" })
		if at < 0 {
			t.Fatal("no project-users migration")
		}
		db := mustOpen(t, dir, opts(migrations[:at]))
		formatDisk(t, db, workDisk)
		must(t, inTx(t, db, func(tx *ProjectTx) error { return tx.Insert(projectRow(idA, "/a")) }))
		_ = db.Close()

		db = mustOpen(t, dir, opts(migrations))
		if n := nextID(t, db); n != 10000 {
			t.Errorf("next_id = %d, want 10000", n)
		}
		if n := count(t, db.Path(), "projects"); n != 1 {
			t.Errorf("projects = %d, want 1", n)
		}
		if u := userOf(t, db, idA, workDisk.Slot); u.UID != 10000 {
			t.Errorf("first UID after the upgrade = %d, want 10000", u.UID)
		}
	})
}

// TestProjectUserAllocation: one number per user, as UID and GID, from
// one counter across projects and data disks, with the names and home
// taken from it (S06-03-image-updates, "Allocation";
// SEC08-proj-isolation).
func TestProjectUserAllocation(t *testing.T) {
	db := newUsersDB(t)
	a, created, err := db.ProjectUser(t.Context(), idA, workDisk.Slot)
	must(t, err)
	if !created {
		t.Error("the first user wasn't made")
	}
	again, created, err := db.ProjectUser(t.Context(), idA, workDisk.Slot)
	must(t, err)
	if created || again != a {
		t.Errorf("second call = %+v (%t), want %+v unchanged", again, created, a)
	}
	b := userOf(t, db, idB, workDisk.Slot)
	aIso := userOf(t, db, idA, isolatedDisk.Slot)

	want := []uint32{10000, 10001, 10002}
	for i, u := range []ProjectUser{a, b, aIso} {
		if u.UID != want[i] || u.GID != u.UID {
			t.Errorf("user %d: UID %d GID %d, want both %d", i, u.UID, u.GID, want[i])
		}
		name := projectuser.UserName(u.UID)
		if u.UserName != name || u.GroupName != name || u.HomePath != "/private/var/wbhomes/"+name {
			t.Errorf("user %d: names %q %q, home %q", i, u.UserName, u.GroupName, u.HomePath)
		}
	}
	if aIso.VolumeUUID != uuidB || a.VolumeUUID != uuidA {
		t.Errorf("volumes %s %s", a.VolumeUUID, aIso.VolumeUUID)
	}
	if n := nextID(t, db); n != 10003 {
		t.Errorf("next_id = %d, want 10003", n)
	}
}

// TestProjectUserGeneratedUIDs: two users have four distinct
// GeneratedUIDs, each an uppercase version 4 UUID.
func TestProjectUserGeneratedUIDs(t *testing.T) {
	db := newUsersDB(t)
	a := userOf(t, db, idA, workDisk.Slot)
	b := userOf(t, db, idB, workDisk.Slot)
	guids := map[string]bool{}
	for _, g := range []string{a.UserGeneratedUID, a.GroupGeneratedUID, b.UserGeneratedUID, b.GroupGeneratedUID} {
		if !projectuser.ValidGeneratedUID(g) {
			t.Errorf("GeneratedUID %q isn't an uppercase version 4 UUID", g)
		}
		guids[g] = true
	}
	if len(guids) != 4 {
		t.Errorf("%d distinct GeneratedUIDs for two users, want 4", len(guids))
	}
}

// TestProjectUserCountsOnAfterReopen: a reopened database goes on from
// its counter.
func TestProjectUserCountsOnAfterReopen(t *testing.T) {
	dir := t.TempDir()
	db := mustOpen(t, dir, opts(migrations))
	formatDisk(t, db, workDisk)
	must(t, inTx(t, db, func(tx *ProjectTx) error {
		if err := tx.Insert(projectRow(idA, "/a")); err != nil {
			return err
		}
		return tx.Insert(projectRow(idB, "/b"))
	}))
	userOf(t, db, idA, workDisk.Slot)
	_ = db.Close()
	db = mustOpen(t, dir, opts(migrations))
	if u := userOf(t, db, idB, workDisk.Slot); u.UID != 10001 {
		t.Errorf("UID after reopen = %d, want 10001", u.UID)
	}
}

// TestProjectUserReregistered: a removed project's IDs, names and
// GeneratedUIDs stay used. Registered again, it gets new ones, and the
// counter is untouched by the removal.
func TestProjectUserReregistered(t *testing.T) {
	db := newUsersDB(t)
	old := userOf(t, db, idA, workDisk.Slot)
	must(t, inTx(t, db, func(tx *ProjectTx) error {
		if _, err := tx.QueueUserRemovals(idA, time.Unix(10, 0)); err != nil {
			return err
		}
		return tx.Delete(idA)
	}))
	if n := nextID(t, db); n != 10001 {
		t.Errorf("next_id after removal = %d, want 10001", n)
	}
	must(t, inTx(t, db, func(tx *ProjectTx) error { return tx.Insert(projectRow(idA, "/a")) }))
	u := userOf(t, db, idA, workDisk.Slot)
	if u.UID == old.UID || u.UserName == old.UserName || u.HomePath == old.HomePath ||
		u.UserGeneratedUID == old.UserGeneratedUID || u.GroupGeneratedUID == old.GroupGeneratedUID {
		t.Errorf("re-registered user %+v shares a value with the removed %+v", u, old)
	}
}

// TestProjectUserCeiling: the last ID is handed out, and the one after
// it is refused with its rule, with nothing written.
func TestProjectUserCeiling(t *testing.T) {
	db := newUsersDB(t)
	_, err := db.db.ExecContext(t.Context(), `UPDATE project_user_ids SET next_id = ?`, int64(projectuser.LastID))
	must(t, err)
	if u := userOf(t, db, idA, workDisk.Slot); u.UID != projectuser.LastID {
		t.Errorf("UID = %d, want LastID", u.UID)
	}
	_, _, err = db.ProjectUser(t.Context(), idB, workDisk.Slot)
	wantErr(t, "past the ceiling", err, ErrUserIDsExhausted)
	if err == nil || !strings.Contains(err.Error(), projectuser.RuleIDsExhausted) {
		t.Errorf("error %v doesn't name the rule", err)
	}
	if n := count(t, db.Path(), "project_users"); n != 1 {
		t.Errorf("project_users = %d, want 1", n)
	}
}

// TestProjectUserRefusals: no user without a formatted data disk or a
// project row, and no counter step for either.
func TestProjectUserRefusals(t *testing.T) {
	db := newProjectsDB(t)
	must(t, inTx(t, db, func(tx *ProjectTx) error { return tx.Insert(projectRow(idA, "/a")) }))
	_, _, err := db.ProjectUser(t.Context(), idA, workDisk.Slot)
	wantErr(t, "no disk", err, ErrDataDiskNotRecorded)
	must(t, db.RecordDataDiskFormat(t.Context(), workDisk))
	_, _, err = db.ProjectUser(t.Context(), idA, workDisk.Slot)
	wantErr(t, "unformatted disk", err, ErrDataDiskNotRecorded)
	must(t, db.MarkDataDiskFormatted(t.Context(), workDisk.Slot, uuidA))
	_, _, err = db.ProjectUser(t.Context(), idB, workDisk.Slot)
	wantErr(t, "no project", err, ErrProjectNotFound)
	_, _, err = db.ProjectUser(t.Context(), idA, "")
	wantErr(t, "no slot", err, errSlotName)
	if n := nextID(t, db); n != 10000 {
		t.Errorf("next_id = %d, want 10000", n)
	}
}

// TestProjectUsersOfDisk: a slot's users are those of its current data
// disk, and a removed project's aren't among them.
func TestProjectUsersOfDisk(t *testing.T) {
	db := newUsersDB(t)
	a := userOf(t, db, idA, workDisk.Slot)
	b := userOf(t, db, idB, workDisk.Slot)
	userOf(t, db, idA, isolatedDisk.Slot)
	got, err := db.ProjectUsers(t.Context(), workDisk.Slot)
	must(t, err)
	if !slices.Equal(got, []ProjectUser{a, b}) {
		t.Errorf("work users = %+v", got)
	}
	must(t, inTx(t, db, func(tx *ProjectTx) error {
		if _, err := tx.QueueUserRemovals(idB, time.Unix(10, 0)); err != nil {
			return err
		}
		return tx.Delete(idB)
	}))
	got, err = db.ProjectUsers(t.Context(), workDisk.Slot)
	must(t, err)
	if !slices.Equal(got, []ProjectUser{a}) {
		t.Errorf("work users after removing B = %+v", got)
	}
	// A new data disk in the slot has none of the old disk's users.
	must(t, db.DropDataDisk(t.Context(), workDisk.Slot))
	formatDisk(t, db, DataDisk{Slot: workDisk.Slot, VolumeUUID: "22222222-3333-4444-5555-666666666666", TempName: "x", SizeGiB: 1})
	got, err = db.ProjectUsers(t.Context(), workDisk.Slot)
	must(t, err)
	if len(got) != 0 {
		t.Errorf("users on a new disk = %+v", got)
	}
	if u := userOf(t, db, idA, workDisk.Slot); u.UID != 10003 {
		t.Errorf("A's user on the new disk has UID %d, want 10003", u.UID)
	}
}

// TestUserRemovalQueue: removing a project queues one removal per data
// disk it had a user on, in its transaction. A clear takes only the
// removal of its own project and UID.
func TestUserRemovalQueue(t *testing.T) {
	db := newUsersDB(t)
	a := userOf(t, db, idA, workDisk.Slot)
	aIso := userOf(t, db, idA, isolatedDisk.Slot)
	b := userOf(t, db, idB, workDisk.Slot)
	var queued []UserRemoval
	must(t, inTx(t, db, func(tx *ProjectTx) error {
		var err error
		if queued, err = tx.QueueUserRemovals(idA, time.Unix(10, 0)); err != nil {
			return err
		}
		return tx.Delete(idA)
	}))
	if len(queued) != 2 {
		t.Fatalf("queued %+v, want 2", queued)
	}
	work, err := db.UserRemovals(t.Context(), workDisk.Slot)
	must(t, err)
	want := UserRemoval{Slot: workDisk.Slot, ProjectID: idA, UID: a.UID, GID: a.GID, HomePath: a.HomePath, Queued: time.Unix(10, 0).UTC()}
	if len(work) != 1 || work[0] != want {
		t.Errorf("work removals = %+v, want %+v", work, want)
	}
	iso, err := db.UserRemovals(t.Context(), isolatedDisk.Slot)
	must(t, err)
	if len(iso) != 1 || iso[0].UID != aIso.UID {
		t.Errorf("isolated removals = %+v", iso)
	}

	// Another project's or another UID's clear leaves it.
	for _, c := range []struct {
		project string
		uid     uint32
	}{{idB, a.UID}, {idB, b.UID}, {idA, b.UID}, {idA, aIso.UID}} {
		ok, err := db.ClearUserRemoval(t.Context(), workDisk.Slot, c.project, c.uid)
		must(t, err)
		if ok {
			t.Errorf("clear of %s %d took A's removal", c.project, c.uid)
		}
	}
	ok, err := db.ClearUserRemoval(t.Context(), workDisk.Slot, idA, a.UID)
	must(t, err)
	if !ok {
		t.Error("A's own clear found nothing")
	}
	if n := count(t, db.Path(), "user_removals"); n != 1 {
		t.Errorf("removals left = %d, want 1", n)
	}
}

// TestProjectDeleteKeepsUserRows: a project with users can't lose its
// row unless its users are queued for removal first.
func TestProjectDeleteKeepsUserRows(t *testing.T) {
	db := newUsersDB(t)
	userOf(t, db, idA, workDisk.Slot)
	err := inTx(t, db, func(tx *ProjectTx) error { return tx.Delete(idA) })
	if err == nil {
		t.Fatal("a project with a user was deleted without its removal")
	}
	if errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("err = %v", err)
	}
}
