// Package guesttools is the guest's end of the guest tools disk: where
// the disk is mounted, the checks that the mount is the disk wb-hostd
// attached, where its programs are installed, and how wb-guestd runs the
// installed wb-guestd in its own place (S06-12-guest-agent, "Update at
// start"). internal/guestupdate runs the update with it.
//
// It is in internal/platform because each of these is per OS
// (S12-01-platform-interfaces, "Rules"). Linux and Windows guests are
// later, and there every call returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
package guesttools

// VolumeName is the name of the tools disk's volume
// (scripts/app-bundle.sh, step 5).
const VolumeName = "wb-guest-tools"

// The rules of this package, for the log.
const (
	// RuleNotMounted: no volume is mounted at the mount point.
	RuleNotMounted = "guestd-tools-not-mounted"
	// RuleUntrusted: the volume at the mount point isn't the disk wb-hostd
	// attached: it is writable, a user mounted it, or it isn't APFS.
	RuleUntrusted = "guestd-tools-untrusted"
	// RuleDirUnsafe: the install directory or a parent of it is a
	// symbolic link or not a directory, isn't owned by root, or its group
	// or others can write it.
	RuleDirUnsafe = "guestd-install-dir-unsafe"
)

// Refusal is the error of a refusal in this package.
type Refusal struct {
	Rule string
	Path string
	msg  string
}

func (r *Refusal) Error() string { return "guesttools: " + r.Path + ": " + r.msg + " (" + r.Rule + ")" }
