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
