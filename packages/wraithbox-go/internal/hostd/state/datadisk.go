package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// createDataDisks is version 2: one row per VM slot with the record of
// its data disk (S06-13-data-disk-format, "Formatting a data disk"). wb-hostd
// writes the volume UUID and the temporary name at step 7, the
// formatted flag at step 9, and the time the VM first started with the
// disk, which ends the SEC15-no-guest-disk-mount exception (S06-13-data-disk-format,
// "The one exception"). A VM starts only with a disk recorded as
// formatted.
func createDataDisks(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE data_disks (
		slot TEXT PRIMARY KEY NOT NULL CHECK (length(slot) BETWEEN 1 AND 64),
		volume_uuid TEXT NOT NULL CHECK (length(volume_uuid) = 36),
		temp_name TEXT NOT NULL CHECK (length(temp_name) BETWEEN 1 AND 255),
		size_gib INTEGER NOT NULL CHECK (size_gib >= 1),
		formatted INTEGER NOT NULL CHECK (formatted IN (0, 1)),
		first_started_unix INTEGER CHECK (first_started_unix IS NULL OR formatted = 1)
	) STRICT`)
	return err
}

// addDataDiskFirstMount is version 4: the time wb-guestd first mounted
// the slot's formatted disk. Until then wb-hostd asks it to make the
// volume's root owned by root:wheel (S06-05-disks, "Owners and
// modes").
func addDataDiskFirstMount(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE data_disks ADD COLUMN
		first_mounted_unix INTEGER CHECK (first_mounted_unix IS NULL OR formatted = 1)`)
	return err
}

// DataDisk is the record of a slot's data disk.
type DataDisk struct {
	Slot string
	// VolumeUUID is the APFS volume's UUID in canonical form, which
	// wb-guestd mounts the volume by (S06-05-disks, "The mount").
	VolumeUUID string
	// TempName is the file's temporary name in the bundle at step 7.
	TempName string
	// SizeGiB is the nominal size the disk was made with.
	SizeGiB uint32
	// Formatted is set at step 9, after the rename to the final name.
	Formatted bool
	// FirstStarted is when the VM first started with the disk, zero
	// before.
	FirstStarted time.Time
	// FirstMounted is when wb-guestd first mounted the disk's volume,
	// zero before.
	FirstMounted time.Time
}

// The data disk errors.
var (
	// ErrDataDiskFormatted: step 7 for a slot whose disk is recorded as
	// formatted. Only a reset drops that record.
	ErrDataDiskFormatted = errors.New("state: the slot's data disk is recorded as formatted")
	// ErrDataDiskNotRecorded: step 9, or the first start, for a slot
	// without the record it needs.
	ErrDataDiskNotRecorded = errors.New("state: the slot has no matching data disk record")
)

// DataDiskRecord returns slot's data disk record, and whether there is
// one.
func (d *DB) DataDiskRecord(ctx context.Context, slot string) (DataDisk, bool, error) {
	if slot == "" || len(slot) > 64 {
		return DataDisk{}, false, errSlotName
	}
	var (
		rec       = DataDisk{Slot: slot}
		size      int64
		formatted int64
		started   sql.NullInt64
		mounted   sql.NullInt64
	)
	err := d.db.QueryRowContext(ctx, `SELECT volume_uuid, temp_name, size_gib, formatted, first_started_unix,
		first_mounted_unix FROM data_disks WHERE slot = ?`, slot).Scan(
		&rec.VolumeUUID, &rec.TempName, &size, &formatted, &started, &mounted)
	if errors.Is(err, sql.ErrNoRows) {
		return DataDisk{}, false, nil
	}
	if err != nil {
		return DataDisk{}, false, err
	}
	if size < 1 || size > 1<<31 {
		return DataDisk{}, false, fmt.Errorf("state: slot %s has a data disk of %d GiB", slot, size)
	}
	rec.SizeGiB = uint32(size)
	rec.Formatted = formatted == 1
	if started.Valid {
		rec.FirstStarted = time.Unix(started.Int64, 0).UTC()
	}
	if mounted.Valid {
		rec.FirstMounted = time.Unix(mounted.Int64, 0).UTC()
	}
	return rec, true, nil
}

// RecordDataDiskFormat is step 7: it records rec's volume UUID,
// temporary name and size for its slot, not formatted. It replaces a
// record that isn't formatted, as one a crash left before step 9, and
// fails with ErrDataDiskFormatted on a formatted one.
func (d *DB) RecordDataDiskFormat(ctx context.Context, rec DataDisk) error {
	if rec.Slot == "" || len(rec.Slot) > 64 {
		return errSlotName
	}
	res, err := d.db.ExecContext(ctx, `INSERT INTO data_disks
		(slot, volume_uuid, temp_name, size_gib, formatted, first_started_unix) VALUES (?, ?, ?, ?, 0, NULL)
		ON CONFLICT (slot) DO UPDATE SET volume_uuid = excluded.volume_uuid, temp_name = excluded.temp_name,
			size_gib = excluded.size_gib, formatted = 0, first_started_unix = NULL, first_mounted_unix = NULL
		WHERE data_disks.formatted = 0`,
		rec.Slot, rec.VolumeUUID, rec.TempName, int64(rec.SizeGiB))
	if err != nil {
		return err
	}
	return oneRow(res, ErrDataDiskFormatted)
}

// MarkDataDiskFormatted is step 9: it records slot's disk with
// volumeUUID as formatted. A record with another UUID, or one already
// formatted, fails with ErrDataDiskNotRecorded.
func (d *DB) MarkDataDiskFormatted(ctx context.Context, slot, volumeUUID string) error {
	res, err := d.db.ExecContext(ctx, `UPDATE data_disks SET formatted = 1
		WHERE slot = ? AND volume_uuid = ? AND formatted = 0`, slot, volumeUUID)
	if err != nil {
		return err
	}
	return oneRow(res, ErrDataDiskNotRecorded)
}

// MarkDataDiskStarted records when the VM first started with slot's
// formatted disk. A later start keeps the first time. A slot without a
// formatted disk fails with ErrDataDiskNotRecorded.
func (d *DB) MarkDataDiskStarted(ctx context.Context, slot string, at time.Time) error {
	res, err := d.db.ExecContext(ctx, `UPDATE data_disks SET first_started_unix = coalesce(first_started_unix, ?)
		WHERE slot = ? AND formatted = 1`, at.Unix(), slot)
	if err != nil {
		return err
	}
	return oneRow(res, ErrDataDiskNotRecorded)
}

// MarkDataDiskMounted records when wb-guestd first mounted slot's
// formatted disk with volumeUUID. A later mount keeps the first time. A
// slot without a formatted disk with that UUID fails with
// ErrDataDiskNotRecorded.
func (d *DB) MarkDataDiskMounted(ctx context.Context, slot, volumeUUID string, at time.Time) error {
	res, err := d.db.ExecContext(ctx, `UPDATE data_disks SET first_mounted_unix = coalesce(first_mounted_unix, ?)
		WHERE slot = ? AND volume_uuid = ? AND formatted = 1`, at.Unix(), slot, volumeUUID)
	if err != nil {
		return err
	}
	return oneRow(res, ErrDataDiskNotRecorded)
}

// DropDataDisk deletes slot's data disk record, if there is one.
func (d *DB) DropDataDisk(ctx context.Context, slot string) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM data_disks WHERE slot = ?`, slot)
	return err
}

// oneRow returns nil when res changed exactly one row, and none
// otherwise.
func oneRow(res sql.Result, none error) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return none
	}
	return nil
}
