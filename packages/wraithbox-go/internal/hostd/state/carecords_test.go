package state

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	caIDA = "0123456789abcdef0123456789abcdef"
	caIDB = "fedcba9876543210fedcba9876543210"
)

func testCA(id, slot string, issued time.Time) CA {
	return CA{
		ID: id, Slot: slot, Certificate: []byte{0x30, 1, 2}, SHA256: strings.Repeat(id[:2], 32),
		Issued: issued, NotBefore: issued.Add(-5 * time.Minute), NotAfter: issued.Add(60 * 24 * time.Hour),
	}
}

func inCATx(t *testing.T, db *DB, fn func(*CATx) error) {
	t.Helper()
	must(t, db.CAs(t.Context(), fn))
}

func listCAs(t *testing.T, db *DB, slot string) []CA {
	t.Helper()
	var out []CA
	inCATx(t, db, func(tx *CATx) error {
		var err error
		out, err = tx.List(slot)
		return err
	})
	return out
}

func removals(t *testing.T, db *DB) []Removal {
	t.Helper()
	var out []Removal
	inCATx(t, db, func(tx *CATx) error {
		var err error
		out, err = tx.Removals()
		return err
	})
	return out
}

// TestCARecordsMigration: the tables are on a new database, and on one
// upgraded from the version before, whose rows stay.
func TestCARecordsMigration(t *testing.T) {
	t.Run("new", func(t *testing.T) {
		db := newProjectsDB(t)
		for _, table := range []string{"ca_records", "ca_removals"} {
			if !hasTable(t, db.Path(), table) {
				t.Errorf("no table %s", table)
			}
		}
	})
	t.Run("upgraded", func(t *testing.T) {
		dir := t.TempDir()
		at := slices.IndexFunc(migrations, func(m migration) bool { return m.name == "ca-records" })
		if at != len(migrations)-1 {
			t.Fatalf("ca-records is migration %d of %d", at, len(migrations))
		}
		db := mustOpen(t, dir, opts(migrations[:at]))
		formatDisk(t, db, workDisk)
		if hasTable(t, db.Path(), "ca_records") {
			t.Fatal("ca_records before its migration")
		}
		_ = db.Close()

		db = mustOpen(t, dir, opts(migrations))
		if n := count(t, db.Path(), "data_disks"); n != 1 {
			t.Errorf("data_disks = %d, want 1", n)
		}
		if n := count(t, db.Path(), "ca_records"); n != 0 {
			t.Errorf("ca_records = %d, want 0", n)
		}
		inCATx(t, db, func(tx *CATx) error { return tx.Insert(testCA(caIDA, "work", time.Unix(1000, 0))) })
		if got := listCAs(t, db, "work"); len(got) != 1 {
			t.Errorf("CAs = %d, want 1", len(got))
		}
	})
}

// summary is slot's CAs, oldest first, each as its id's first two
// characters with " signing" and " installed <generation>" where set.
func summary(t *testing.T, db *DB, slot string) string {
	t.Helper()
	var parts []string
	for _, ca := range listCAs(t, db, slot) {
		s := ca.ID[:2]
		if ca.Signing {
			s += " signing"
		}
		if ca.InstallGeneration != 0 {
			s += fmt.Sprintf(" installed %d", ca.InstallGeneration)
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func wantSummary(t *testing.T, db *DB, slot, want string) {
	t.Helper()
	if got := summary(t, db, slot); got != want {
		t.Errorf("%s CAs = %q, want %q", slot, got, want)
	}
}

// TestCARecord: insert, install, signing and drop, read back.
func TestCARecord(t *testing.T) {
	db := newProjectsDB(t)
	day := 24 * time.Hour
	start := time.Unix(1_800_000_000, 0).UTC()
	inCATx(t, db, func(tx *CATx) error {
		if err := tx.Insert(testCA(caIDB, "work", start.Add(30*day))); err != nil {
			return err
		}
		return tx.Insert(testCA(caIDA, "work", start))
	})
	wantSummary(t, db, "work", "01, fe")
	wantSummary(t, db, "isolated", "")
	got := listCAs(t, db, "work")
	if !got[0].Issued.Equal(start) || !got[0].NotAfter.Equal(start.Add(60*day)) || !got[0].Installed.IsZero() {
		t.Errorf("A = %+v", got[0])
	}

	inCATx(t, db, func(tx *CATx) error {
		if err := tx.MarkInstalled("work", []string{caIDA, caIDB}, 4, start.Add(time.Hour)); err != nil {
			return err
		}
		return tx.SetSigning("work", caIDA)
	})
	wantSummary(t, db, "work", "01 signing installed 4, fe installed 4")
	if got = listCAs(t, db, "work"); !got[1].Installed.Equal(start.Add(time.Hour)) {
		t.Errorf("installed at %v", got[1].Installed)
	}
	inCATx(t, db, func(tx *CATx) error { return tx.SetSigning("work", caIDB) })
	wantSummary(t, db, "work", "01 installed 4, fe signing installed 4")
	inCATx(t, db, func(tx *CATx) error { return tx.SetSigning("work", "") })
	wantSummary(t, db, "work", "01 installed 4, fe installed 4")

	item := "wraithbox/ca/work/" + caIDA
	inCATx(t, db, func(tx *CATx) error { return tx.Drop("work", caIDA, item, RemovalExpired, start.Add(60*day)) })
	wantSummary(t, db, "work", "fe installed 4")
	if r := removals(t, db); len(r) != 1 || r[0].Item != item || r[0].Reason != RemovalExpired {
		t.Errorf("removals = %+v", r)
	}
}

// TestCARecordRefused feeds the record changes its checks refuse, and
// sees each fail and change nothing.
func TestCARecordRefused(t *testing.T) {
	db := newProjectsDB(t)
	at := time.Unix(1_800_000_000, 0)
	inCATx(t, db, func(tx *CATx) error { return tx.Insert(testCA(caIDA, "work", at)) })
	bad := func(edit func(*CA)) CA {
		ca := testCA(caIDB, "work", at)
		edit(&ca)
		return ca
	}
	for name, fn := range map[string]func(*CATx) error{
		"id twice":          func(tx *CATx) error { return tx.Insert(testCA(caIDA, "isolated", at)) },
		"fingerprint twice": func(tx *CATx) error { return tx.Insert(bad(func(c *CA) { c.SHA256 = strings.Repeat("01", 32) })) },
		"short id":          func(tx *CATx) error { return tx.Insert(bad(func(c *CA) { c.ID = caIDB[:31] })) },
		"upper-case id":     func(tx *CATx) error { return tx.Insert(bad(func(c *CA) { c.ID = strings.ToUpper(caIDB) })) },
		"slot with a slash": func(tx *CATx) error { return tx.Insert(bad(func(c *CA) { c.Slot = "work/x" })) },
		"empty slot":        func(tx *CATx) error { return tx.Insert(bad(func(c *CA) { c.Slot = "" })) },
		"no certificate":    func(tx *CATx) error { return tx.Insert(bad(func(c *CA) { c.Certificate = nil })) },
		"long certificate":  func(tx *CATx) error { return tx.Insert(bad(func(c *CA) { c.Certificate = make([]byte, 1025) })) },
		"bad fingerprint":   func(tx *CATx) error { return tx.Insert(bad(func(c *CA) { c.SHA256 = "ab" })) },
		"ends before start": func(tx *CATx) error { return tx.Insert(bad(func(c *CA) { c.NotAfter = c.NotBefore })) },
		"install of another slot": func(tx *CATx) error {
			return tx.MarkInstalled("isolated", []string{caIDA}, 1, at)
		},
		"install generation 0":  func(tx *CATx) error { return tx.MarkInstalled("work", []string{caIDA}, 0, at) },
		"signing another slot":  func(tx *CATx) error { return tx.SetSigning("isolated", caIDA) },
		"drop of another slot":  func(tx *CATx) error { return tx.Drop("isolated", caIDA, "wraithbox/ca/isolated/x", RemovalResetCA, at) },
		"removal of a non-CA":   func(tx *CATx) error { return tx.AddRemoval("wraithbox/binding/x", RemovalResetCA, at) },
		"removal, other reason": func(tx *CATx) error { return tx.AddRemoval("wraithbox/ca/work/"+caIDB, "rotated", at) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := db.CAs(t.Context(), fn); err == nil {
				t.Fatal("accepted")
			}
			if got := listCAs(t, db, "work"); len(got) != 1 || got[0].InstallGeneration != 0 || got[0].Signing {
				t.Errorf("record changed: %+v", got)
			}
			if r := removals(t, db); len(r) != 0 {
				t.Errorf("removals = %+v", r)
			}
		})
	}
	err := db.CAs(t.Context(), func(tx *CATx) error { return tx.SetSigning("work", caIDB) })
	if !errors.Is(err, ErrCANotRecorded) {
		t.Errorf("signing an unknown CA: %v, want ErrCANotRecorded", err)
	}
}

// TestCARemovals: the list is oldest first, an item added twice keeps
// its first reason, and a dropped item leaves.
func TestCARemovals(t *testing.T) {
	db := newProjectsDB(t)
	a, b := "wraithbox/ca/work/"+caIDA, "wraithbox/ca/isolated/"+caIDB
	inCATx(t, db, func(tx *CATx) error {
		if err := tx.AddRemoval(b, RemovalFailedIssue, time.Unix(20, 0)); err != nil {
			return err
		}
		if err := tx.AddRemoval(a, RemovalExpired, time.Unix(10, 0)); err != nil {
			return err
		}
		return tx.AddRemoval(a, RemovalResetCA, time.Unix(30, 0))
	})
	r := removals(t, db)
	if len(r) != 2 || r[0].Item != a || r[0].Reason != RemovalExpired || r[1].Item != b {
		t.Fatalf("removals = %+v", r)
	}
	inCATx(t, db, func(tx *CATx) error {
		if err := tx.DropRemoval(a); err != nil {
			return err
		}
		return tx.DropRemoval("wraithbox/ca/work/gone")
	})
	if r = removals(t, db); len(r) != 1 || r[0].Item != b {
		t.Errorf("after the drop = %+v", r)
	}
}
