package state

import (
	"errors"
	"testing"
	"time"
)

const (
	uuidA = "0F1E2D3C-4B5A-6978-8796-A5B4C3D2E1F0"
	uuidB = "11111111-2222-3333-4444-555555555555"
)

func newDataDiskDB(t *testing.T) *DB {
	t.Helper()
	db := mustOpen(t, t.TempDir(), opts(migrations))
	if !hasTable(t, db.Path(), "data_disks") {
		t.Fatal("data_disks missing")
	}
	return db
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Errorf("%s: err = %v, want %v", what, err, want)
	}
}

var workDisk = DataDisk{Slot: "macos-work", VolumeUUID: uuidA, TempName: "data.wbdisk.tmp", SizeGiB: 128}

// TestDataDiskSteps: the records of steps 7 and 9 (S06-13-data-disk-format,
// "Formatting a data disk"). A record a crash left before step 9 is
// replaced by the next format, and a formatted one isn't.
func TestDataDiskSteps(t *testing.T) {
	ctx := t.Context()
	db := newDataDiskDB(t)
	if _, ok, err := db.DataDiskRecord(ctx, "macos-work"); ok || err != nil {
		t.Fatalf("a new database has a record (%t, %v)", ok, err)
	}
	must(t, db.RecordDataDiskFormat(ctx, workDisk))
	replaced := workDisk
	replaced.VolumeUUID = uuidB
	must(t, db.RecordDataDiskFormat(ctx, replaced))
	wantErr(t, "step 9 with the replaced UUID", db.MarkDataDiskFormatted(ctx, "macos-work", uuidA), ErrDataDiskNotRecorded)
	wantErr(t, "a start before step 9", db.MarkDataDiskStarted(ctx, "macos-work", time.Unix(10, 0)), ErrDataDiskNotRecorded)
	must(t, db.MarkDataDiskFormatted(ctx, "macos-work", uuidB))
	wantErr(t, "step 9 twice", db.MarkDataDiskFormatted(ctx, "macos-work", uuidB), ErrDataDiskNotRecorded)
	wantErr(t, "step 7 over a formatted disk", db.RecordDataDiskFormat(ctx, workDisk), ErrDataDiskFormatted)
	got, ok, err := db.DataDiskRecord(ctx, "macos-work")
	must(t, err)
	want := replaced
	want.Formatted = true
	if !ok || got != want {
		t.Errorf("record = %+v (%t), want %+v", got, ok, want)
	}
	if _, ok, _ := db.DataDiskRecord(ctx, "macos-isolated"); ok {
		t.Error("the other slot has a record")
	}
}

// TestDataDiskFirstStart: the first start's time is kept.
func TestDataDiskFirstStart(t *testing.T) {
	ctx := t.Context()
	db := newDataDiskDB(t)
	must(t, db.RecordDataDiskFormat(ctx, workDisk))
	must(t, db.MarkDataDiskFormatted(ctx, "macos-work", uuidA))
	for _, at := range []int64{100, 200} {
		must(t, db.MarkDataDiskStarted(ctx, "macos-work", time.Unix(at, 0)))
	}
	got, _, err := db.DataDiskRecord(ctx, "macos-work")
	must(t, err)
	if !got.FirstStarted.Equal(time.Unix(100, 0)) {
		t.Errorf("first start %v, want the first", got.FirstStarted)
	}
}

// TestDataDiskFirstMount: the first mount's time is kept, needs the
// formatted disk with the UUID, and a new format clears it.
func TestDataDiskFirstMount(t *testing.T) {
	ctx := t.Context()
	db := newDataDiskDB(t)
	must(t, db.RecordDataDiskFormat(ctx, workDisk))
	wantErr(t, "a mount before step 9", db.MarkDataDiskMounted(ctx, "macos-work", uuidA, time.Unix(10, 0)), ErrDataDiskNotRecorded)
	must(t, db.MarkDataDiskFormatted(ctx, "macos-work", uuidA))
	wantErr(t, "a mount of another volume", db.MarkDataDiskMounted(ctx, "macos-work", uuidB, time.Unix(10, 0)), ErrDataDiskNotRecorded)
	for _, at := range []int64{100, 200} {
		must(t, db.MarkDataDiskMounted(ctx, "macos-work", uuidA, time.Unix(at, 0)))
	}
	got, _, err := db.DataDiskRecord(ctx, "macos-work")
	must(t, err)
	if !got.FirstMounted.Equal(time.Unix(100, 0)) {
		t.Errorf("first mount %v, want the first", got.FirstMounted)
	}
	must(t, db.DropDataDisk(ctx, "macos-work"))
	must(t, db.RecordDataDiskFormat(ctx, workDisk))
	if got, _, _ := db.DataDiskRecord(ctx, "macos-work"); !got.FirstMounted.IsZero() {
		t.Errorf("a new format has a first mount at %v", got.FirstMounted)
	}
}

// TestDataDiskFirstMountMigration: version 4 adds the first mount to a
// version 2 record, unset.
func TestDataDiskFirstMountMigration(t *testing.T) {
	dir := t.TempDir()
	fixture(t, dir, 2, `CREATE TABLE vm_slots (slot TEXT PRIMARY KEY NOT NULL, last_generation INTEGER NOT NULL) STRICT`,
		`CREATE TABLE data_disks (
			slot TEXT PRIMARY KEY NOT NULL, volume_uuid TEXT NOT NULL, temp_name TEXT NOT NULL,
			size_gib INTEGER NOT NULL, formatted INTEGER NOT NULL, first_started_unix INTEGER
		) STRICT`,
		`INSERT INTO data_disks VALUES ('macos-work', '`+uuidA+`', 'data.wbdisk.tmp', 128, 1, 50)`)
	db := mustOpen(t, dir, opts(migrations))
	got, ok, err := db.DataDiskRecord(t.Context(), "macos-work")
	must(t, err)
	if !ok || !got.Formatted || !got.FirstMounted.IsZero() || !got.FirstStarted.Equal(time.Unix(50, 0)) {
		t.Fatalf("record after the migration = %+v (%t)", got, ok)
	}
	must(t, db.MarkDataDiskMounted(t.Context(), "macos-work", uuidA, time.Unix(60, 0)))
}

// TestDataDiskDrop: a dropped record is gone, and a new format records
// again.
func TestDataDiskDrop(t *testing.T) {
	ctx := t.Context()
	db := newDataDiskDB(t)
	must(t, db.RecordDataDiskFormat(ctx, workDisk))
	must(t, db.MarkDataDiskFormatted(ctx, "macos-work", uuidA))
	must(t, db.DropDataDisk(ctx, "macos-work"))
	if _, ok, _ := db.DataDiskRecord(ctx, "macos-work"); ok {
		t.Error("the record is still there after the drop")
	}
	must(t, db.RecordDataDiskFormat(ctx, workDisk))
}

func TestDataDiskChecks(t *testing.T) {
	ctx := t.Context()
	db := newDataDiskDB(t)
	for name, rec := range map[string]DataDisk{
		"no slot":    {VolumeUUID: uuidA, TempName: "t", SizeGiB: 8},
		"short uuid": {Slot: "macos-work", VolumeUUID: "x", TempName: "t", SizeGiB: 8},
		"no name":    {Slot: "macos-work", VolumeUUID: uuidA, SizeGiB: 8},
		"no size":    {Slot: "macos-work", VolumeUUID: uuidA, TempName: "t"},
	} {
		if err := db.RecordDataDiskFormat(ctx, rec); err == nil {
			t.Errorf("%s: recorded", name)
		}
	}
}
