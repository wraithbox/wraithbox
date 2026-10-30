// Package launch is the table of fixed launcher entries: what wb-hostd
// may start, each with its program, its fixed environment and its
// profile parameter (S04-03-self-sandboxing, "`wb-launcher`";
// S04-04-vm-processes). Until wb-launcher exists (M3.11), wb-hostd
// starts an entry itself, with exactly what the entry holds. M3.11 moves
// this table into wb-launcher, and with it the kill of wb-diskfmt's
// process group (S04-04-vm-processes, "`wb-diskfmt`'s kill").
//
// No entry takes an argument, and no request names a path, a program or
// a variable: the caller names an entry, and everything else comes from
// here.
package launch

import (
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/diskfmt"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/vmbundle"
)

// Entry is one fixed launcher entry.
type Entry struct {
	// Name is the entry's name, such as wb-diskfmt-work.
	Name string
	// Program is the program's name in the installed bundle's
	// Contents/MacOS (S17-01-bundle).
	Program string
	// Env is the program's whole environment.
	Env []string
	// BundleParam is the profile parameter, the VM's bundle relative to
	// <data>. The program resolves it with realpath before it confines
	// itself.
	BundleParam string
	// Slot is the VM slot the entry belongs to.
	Slot string
}

// DiskfmtProgram is wb-diskfmt's name in the bundle.
const DiskfmtProgram = "wb-diskfmt"

// Entries is the table. A slot's entry has that slot's variable and
// that slot's bundle, so wb-diskfmt never formats another VM's disk
// (SEC08-proj-isolation).
var Entries = []Entry{
	diskfmtEntry(vmbundle.SlotWork),
	diskfmtEntry(vmbundle.SlotIsolated),
}

func diskfmtEntry(slot string) Entry {
	return Entry{
		Name:        DiskfmtProgram + "-" + slot,
		Program:     DiskfmtProgram,
		Env:         []string{diskfmt.SlotEnv + "=" + slot},
		BundleParam: vmbundle.Rel(slot),
		Slot:        slot,
	}
}

// Diskfmt returns the wb-diskfmt entry of slot.
func Diskfmt(slot string) (Entry, bool) {
	for _, e := range Entries {
		if e.Program == DiskfmtProgram && e.Slot == slot {
			return e, true
		}
	}
	return Entry{}, false
}
