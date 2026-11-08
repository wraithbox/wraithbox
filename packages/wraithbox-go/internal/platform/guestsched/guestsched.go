// Package guestsched checks that the guest's cron and at are closed to
// project users, for wb-guestd (S13-01-process-hardening, "Cron and at";
// SEC08-proj-isolation). cron and atrun run as root and start a user's
// jobs as that user, outside its launchd domain. So neither the kill
// loop's SIGKILL nor its launchctl bootout reaches them, and neither
// the disabled account nor its shell stops them.
//
// TCC protects the directory: a root LaunchDaemon such as wb-guestd can
// read it, but can't create or remove a file in it. So the image
// build's SSH session writes cron's and at's allow files
// (S06-02-image-build, "Guest confinement"), and this package only
// reads. CheckAllow checks the allow files. Scan counts the crontabs
// and at jobs of every project user, for wb-guestd's start. System.Find
// counts one user's, for the kill loop. The decisions are in
// internal/guestd and internal/guestd/procs. On darwin the calls are
// openat and fstatat relative to a descriptor of the directory, through
// golang.org/x/sys/unix, and none of them follows a symbolic link.
//
// Linux and Windows guests are later (S12-platforms). Until they are
// built, every call there returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
package guestsched

// Allowed is the whole content each allow file must have: root only.
const Allowed = "root\n"

// The names in the directory, as cron and at read them.
const (
	// CronAllow lists the users crontab accepts. When it exists,
	// crontab ignores cron.deny.
	CronAllow = "cron.allow"
	// AtAllow lists the users at accepts. When it exists, at ignores
	// at.deny.
	AtAllow = "at.allow"
	// Tabs holds each user's crontab, under the user's name.
	Tabs = "tabs"
	// Jobs holds the at jobs. Each file belongs to the user who queued
	// it, and atrun runs it as that user.
	Jobs = "jobs"
)

// MaxEntries bounds how many entries Find and Scan read in the jobs or
// tabs directory (SEC13-bounded-resources). A directory with more fails
// the call: the kill loop then reports the user as not clean, and
// wb-guestd doesn't start.
const MaxEntries = 10000

// Found is what Find or Scan found.
type Found struct {
	// Tabs is how many crontabs of project users are there. Find
	// counts at most one.
	Tabs int
	// Jobs is how many at jobs of project users are there.
	Jobs int
}

// Any reports whether anything was found.
func (f Found) Any() bool { return f.Tabs > 0 || f.Jobs > 0 }

// System is the guest's own cron and at.
type System struct{}
