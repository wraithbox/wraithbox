package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// migration moves the schema one version forward. migrations[i] moves
// it from version i to i+1. The list is forward-only: an entry is never
// changed or removed once released, and a schema change is a new entry
// at the end (S17-05-upgrades-and-migrations, "state.db").
type migration struct {
	name  string
	apply func(ctx context.Context, tx *sql.Tx) error
}

// migrations is the schema. Each package that needs a table adds its own
// migration, so the schema has only what exists.
var migrations = []migration{
	{name: "vm-slots", apply: createVMSlots},
}

// createVMSlots is version 1: one row per VM slot with the last VM
// generation wb-hostd handed out. wb-hostd owns generations: each start
// and each restore begins a new one, from 1 up (S04-02-vm-provider,
// "Restore").
func createVMSlots(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE vm_slots (
		slot TEXT PRIMARY KEY NOT NULL CHECK (length(slot) BETWEEN 1 AND 64),
		last_generation INTEGER NOT NULL CHECK (last_generation >= 1)
	) STRICT`)
	return err
}

// errSlotName is returned for a slot name outside the table's check.
var errSlotName = errors.New("state: a slot name is 1 to 64 bytes")

// NextGeneration hands out the next VM generation of slot, and records
// it as the slot's last. The first is 1. Generations never repeat for a
// slot, across restarts of wb-hostd too (S04-02-vm-provider, "Restore").
func (d *DB) NextGeneration(ctx context.Context, slot string) (uint64, error) {
	if slot == "" || len(slot) > 64 {
		return 0, errSlotName
	}
	var g int64
	err := d.db.QueryRowContext(ctx, `INSERT INTO vm_slots (slot, last_generation) VALUES (?, 1)
		ON CONFLICT (slot) DO UPDATE SET last_generation = last_generation + 1
		RETURNING last_generation`, slot).Scan(&g)
	if err != nil {
		return 0, err
	}
	// The table's check keeps it at 1 or more. Checked again, so a
	// changed table can't hand out 0, which wb-vmd refuses.
	if g < 1 {
		return 0, fmt.Errorf("state: slot %s has generation %d", slot, g)
	}
	return uint64(g), nil
}
