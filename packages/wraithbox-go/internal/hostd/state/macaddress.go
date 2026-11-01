package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
)

// MACLength is the length of a MAC address, in bytes.
const MACLength = 6

// createVMMACAddresses adds one row per VM slot with the MAC address of
// its network device, made on the slot's first start and kept, so the
// guest sees the same device on every start (S06-06-devices-and-warm-start,
// "Devices"). No two slots share one.
func createVMMACAddresses(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE vm_mac_addresses (
		slot TEXT PRIMARY KEY NOT NULL CHECK (length(slot) BETWEEN 1 AND 64),
		mac BLOB NOT NULL UNIQUE CHECK (length(mac) = 6)
	) STRICT`)
	return err
}

// randomMAC fills b with random bytes. A test replaces it.
var randomMAC = func(b []byte) { _, _ = rand.Read(b) }

// macAttempts bounds the new addresses MACAddress tries when the one it
// made is another slot's. With 46 random bits a second try is already
// rare.
const macAttempts = 8

// errMACTaken: every new address MACAddress made was another slot's.
var errMACTaken = errors.New("state: every new MAC address was already another slot's")

// MACAddress returns slot's MAC address, and makes and records one on
// the slot's first call: a random, locally administered unicast address
// that no other slot has. Later calls return the same address, across
// restarts of wb-hostd too (S06-06-devices-and-warm-start, "Devices").
func (d *DB) MACAddress(ctx context.Context, slot string) ([]byte, error) {
	if slot == "" || len(slot) > 64 {
		return nil, errSlotName
	}
	for range macAttempts {
		mac, found, err := d.recordMAC(ctx, slot, newMAC())
		if err != nil {
			return nil, err
		}
		if found {
			return mac, checkMAC(slot, mac)
		}
	}
	return nil, errMACTaken
}

// recordMAC records candidate as slot's address unless the slot has
// one, and returns the slot's address. found is false when the slot has
// none because candidate was another slot's.
func (d *DB) recordMAC(ctx context.Context, slot string, candidate []byte) (mac []byte, found bool, err error) {
	if _, err := d.db.ExecContext(ctx, `INSERT OR IGNORE INTO vm_mac_addresses (slot, mac) VALUES (?, ?)`,
		slot, candidate); err != nil {
		return nil, false, err
	}
	err = d.db.QueryRowContext(ctx, `SELECT mac FROM vm_mac_addresses WHERE slot = ?`, slot).Scan(&mac)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return mac, true, nil
}

// newMAC is a random MAC address with the locally administered bit set
// and the multicast bit clear.
func newMAC() []byte {
	b := make([]byte, MACLength)
	randomMAC(b)
	b[0] = b[0]&^0x01 | 0x02
	return b
}

// checkMAC checks an address read back from the database: wb-vmd
// refuses any other (vmd-mac-invalid).
func checkMAC(slot string, mac []byte) error {
	if len(mac) != MACLength || mac[0]&0x01 != 0 || mac[0]&0x02 == 0 {
		return fmt.Errorf("state: slot %s has the MAC address %x, which isn't a locally administered unicast address", slot, mac)
	}
	return nil
}
