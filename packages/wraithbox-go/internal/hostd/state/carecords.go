package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// createCARecords is the ca-records migration: each VM's inspection CAs
// and the removal list of CA key items (S09-07-ca-certificates,
// "Lifetimes"; S15-03-access-rules, "Removing CA keys").
//
// ca_records has one row per CA in wb-hostd's record. Its slot and CA id
// are parts of the item name `ca/<vm>/<ca>` (internal/storeitem): the
// slot 1 to 64 characters of a to z, 0 to 9 and -, and the id 32
// lowercase hexadecimal characters. The install is recorded with the
// VM generation that reported it and the time, or not at all. At most
// one CA of a slot signs.
//
// ca_removals holds the key items wb-proxyd is still to delete, each
// with the reason it went on the list and when.
func createCARecords(ctx context.Context, tx *sql.Tx) error {
	for _, s := range []string{
		`CREATE TABLE ca_records (
			ca_id TEXT PRIMARY KEY NOT NULL CHECK (length(ca_id) = 32 AND ca_id NOT GLOB '*[^0-9a-f]*'),
			slot TEXT NOT NULL CHECK (length(slot) BETWEEN 1 AND 64 AND slot NOT GLOB '*[^a-z0-9-]*'),
			certificate BLOB NOT NULL CHECK (length(certificate) BETWEEN 1 AND 1024),
			sha256 TEXT NOT NULL UNIQUE CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
			issued_unix INTEGER NOT NULL,
			not_before_unix INTEGER NOT NULL,
			not_after_unix INTEGER NOT NULL CHECK (not_after_unix > not_before_unix),
			install_generation INTEGER CHECK (install_generation >= 1),
			installed_unix INTEGER,
			signing INTEGER NOT NULL CHECK (signing IN (0, 1)),
			CHECK ((install_generation IS NULL) = (installed_unix IS NULL))
		) STRICT`,
		`CREATE UNIQUE INDEX ca_records_signing ON ca_records (slot) WHERE signing = 1`,
		`CREATE TABLE ca_removals (
			item TEXT PRIMARY KEY NOT NULL CHECK (length(item) BETWEEN 1 AND 256 AND item GLOB 'wraithbox/ca/*'),
			reason TEXT NOT NULL CHECK (reason IN ('expired', 'reset-ca', 'failed-issue')),
			added_unix INTEGER NOT NULL
		) STRICT`,
	} {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// CA is a row of ca_records: one inspection CA of a VM.
type CA struct {
	ID   string
	Slot string
	// Certificate is the CA certificate, DER, as wb-proxyd issued it.
	Certificate []byte
	// SHA256 is the certificate's fingerprint, 64 lowercase hexadecimal
	// characters.
	SHA256 string
	// Issued is when wb-hostd recorded the CA. The schedule counts its
	// days from it (S09-07-ca-certificates, "Rotation").
	Issued    time.Time
	NotBefore time.Time
	NotAfter  time.Time
	// InstallGeneration is the VM generation whose guest reported the
	// install, 0 when none did. Installed is when.
	InstallGeneration uint64
	Installed         time.Time
	// Signing is whether wb-proxyd signs the VM's leaves with it.
	Signing bool
}

// The reasons a key item is on the removal list.
const (
	RemovalExpired     = "expired"
	RemovalResetCA     = "reset-ca"
	RemovalFailedIssue = "failed-issue"
)

// Removal is a row of ca_removals.
type Removal struct {
	Item   string
	Reason string
	Added  time.Time
}

// ErrCANotRecorded: a change to a CA that isn't in the record.
var ErrCANotRecorded = errors.New("state: no CA with that id in the slot's record")

// CATx reads and writes the CA record and the removal list in one
// transaction.
type CATx struct {
	ctx context.Context
	tx  *sql.Tx
}

// CAs runs fn in one transaction, which commits when fn returns nil and
// rolls back otherwise, as Projects does.
func (d *DB) CAs(ctx context.Context, fn func(*CATx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&CATx{ctx: ctx, tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

const caColumns = `ca_id, slot, certificate, sha256, issued_unix, not_before_unix, not_after_unix,
	install_generation, installed_unix, signing`

// List returns slot's CAs, oldest first.
func (c *CATx) List(slot string) ([]CA, error) {
	rows, err := c.tx.QueryContext(c.ctx, `SELECT `+caColumns+` FROM ca_records WHERE slot = ?
		ORDER BY issued_unix, not_before_unix, ca_id`, slot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CA
	for rows.Next() {
		ca, err := scanCA(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ca)
	}
	return out, rows.Err()
}

func scanCA(rows *sql.Rows) (CA, error) {
	var (
		ca                          CA
		issued, notBefore, notAfter int64
		gen, installed              sql.NullInt64
		signing                     int64
	)
	if err := rows.Scan(&ca.ID, &ca.Slot, &ca.Certificate, &ca.SHA256, &issued, &notBefore, &notAfter,
		&gen, &installed, &signing); err != nil {
		return CA{}, err
	}
	ca.Issued = time.Unix(issued, 0).UTC()
	ca.NotBefore = time.Unix(notBefore, 0).UTC()
	ca.NotAfter = time.Unix(notAfter, 0).UTC()
	if gen.Valid {
		if gen.Int64 < 1 {
			return CA{}, fmt.Errorf("state: CA %s has install generation %d", ca.ID, gen.Int64)
		}
		ca.InstallGeneration = uint64(gen.Int64)
		ca.Installed = time.Unix(installed.Int64, 0).UTC()
	}
	ca.Signing = signing == 1
	return ca, nil
}

// Insert adds ca to the record, not installed and not signing.
func (c *CATx) Insert(ca CA) error {
	_, err := c.tx.ExecContext(c.ctx, `INSERT INTO ca_records (`+caColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL, NULL, 0)`,
		ca.ID, ca.Slot, ca.Certificate, ca.SHA256, ca.Issued.Unix(), ca.NotBefore.Unix(), ca.NotAfter.Unix())
	return err
}

// MarkInstalled records that the guest of generation installed each of
// slot's CAs in ids, at at. A CA that isn't the slot's fails with
// ErrCANotRecorded.
func (c *CATx) MarkInstalled(slot string, ids []string, generation uint64, at time.Time) error {
	if generation < 1 || generation > 1<<62 {
		return fmt.Errorf("state: install generation %d", generation)
	}
	for _, id := range ids {
		res, err := c.tx.ExecContext(c.ctx, `UPDATE ca_records SET install_generation = ?, installed_unix = ?
			WHERE slot = ? AND ca_id = ?`, int64(generation), at.Unix(), slot, id)
		if err != nil {
			return err
		}
		if err := oneRow(res, ErrCANotRecorded); err != nil {
			return err
		}
	}
	return nil
}

// SetSigning makes id slot's signing CA, or leaves the slot with none
// when id is "". An id that isn't the slot's fails with
// ErrCANotRecorded.
func (c *CATx) SetSigning(slot, id string) error {
	if _, err := c.tx.ExecContext(c.ctx, `UPDATE ca_records SET signing = 0 WHERE slot = ?`, slot); err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	res, err := c.tx.ExecContext(c.ctx, `UPDATE ca_records SET signing = 1 WHERE slot = ? AND ca_id = ?`, slot, id)
	if err != nil {
		return err
	}
	return oneRow(res, ErrCANotRecorded)
}

// Drop deletes slot's CA id from the record and puts item, its key
// item, on the removal list with reason, at at. A CA that isn't the
// slot's fails with ErrCANotRecorded.
func (c *CATx) Drop(slot, id, item, reason string, at time.Time) error {
	res, err := c.tx.ExecContext(c.ctx, `DELETE FROM ca_records WHERE slot = ? AND ca_id = ?`, slot, id)
	if err != nil {
		return err
	}
	if err := oneRow(res, ErrCANotRecorded); err != nil {
		return err
	}
	return c.AddRemoval(item, reason, at)
}

// AddRemoval puts item on the removal list with reason, at at. An item
// already on the list keeps its reason and time.
func (c *CATx) AddRemoval(item, reason string, at time.Time) error {
	_, err := c.tx.ExecContext(c.ctx, `INSERT INTO ca_removals (item, reason, added_unix) VALUES (?, ?, ?)
		ON CONFLICT (item) DO NOTHING`,
		item, reason, at.Unix())
	return err
}

// Removals returns the removal list, oldest first.
func (c *CATx) Removals() ([]Removal, error) {
	rows, err := c.tx.QueryContext(c.ctx, `SELECT item, reason, added_unix FROM ca_removals ORDER BY added_unix, rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Removal
	for rows.Next() {
		var r Removal
		var added int64
		if err := rows.Scan(&r.Item, &r.Reason, &added); err != nil {
			return nil, err
		}
		r.Added = time.Unix(added, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// DropRemoval takes item off the removal list. An item not on it is no
// error.
func (c *CATx) DropRemoval(item string) error {
	_, err := c.tx.ExecContext(c.ctx, `DELETE FROM ca_removals WHERE item = ?`, item)
	return err
}
