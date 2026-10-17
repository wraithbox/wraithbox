// Package vmbundle names the VM slots, each slot's bundle directory under
// <data>, and the fixed file names in a bundle that more than one
// program uses (S04-00-architecture, "Host state"; S06-05-disks,
// "Bundle files"). wb-hostd, wb-diskfmt and wb build the same paths
// from it, so a slot's bundle and its files are named in one place.
//
// It has no OS code and reads nothing.
package vmbundle

import "path/filepath"

// The slots, the values of WB_VMD_SLOT and WB_DISKFMT_SLOT
// (S04-04-vm-processes, "The slot").
const (
	SlotWork     = "work"
	SlotIsolated = "isolated"
)

// Slots returns every slot, in the order a listing shows them.
func Slots() []string { return []string{SlotWork, SlotIsolated} }

// ValidSlot reports whether s is one of Slots.
func ValidSlot(s string) bool { return s == SlotWork || s == SlotIsolated }

// The data disk's names in a bundle (S06-05-disks, "Bundle files", "The
// temporary and final names"). Neither ends in an extension that
// DiskImageMounter claims, so a double click on either doesn't attach
// it on the host (S06-05-disks, "Names on the host";
// SEC15-no-guest-disk-mount). The temporary name is fixed, so wb-hostd
// and wb-diskfmt both recognize a leftover. wb-vmd attaches the final
// name: BundleFile.dataDisk in packages/wraithbox-swift.
const (
	DataDiskName     = "data.wbdisk"
	DataDiskTempName = "data.wbdisk.tmp"
)

// Name is a slot's bundle name without its extension, macos-work or
// macos-isolated. It is also the slot's key in state.db.
func Name(slot string) string { return "macos-" + slot }

// Rel is a slot's bundle path relative to <data>:
// vms/macos-<slot>.noindex. The .noindex extension keeps Spotlight out
// of the disks (S06-05-disks, "Disks").
func Rel(slot string) string { return filepath.Join("vms", Name(slot)+".noindex") }

// Path is a slot's bundle under dataDir, <data>/vms/macos-<slot>.noindex.
// A slot uses its own bundle, never another's (SEC08-proj-isolation).
func Path(dataDir, slot string) string { return filepath.Join(dataDir, Rel(slot)) }
