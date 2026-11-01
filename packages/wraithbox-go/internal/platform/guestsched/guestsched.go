// Package guestsched closes the guest's cron and at to project users,
// for wb-guestd (S13-01-process-hardening, "Cron and at";
// SEC08-proj-isolation). cron and atrun run as root and start a user's
// jobs as that user, outside its launchd domain. So neither the kill
// loop's SIGKILL nor its launchctl bootout reaches them, and neither
// the disabled account nor its shell stops them.
//
// Lock writes cron's and at's allow files with root only, so a project
// user can't add a crontab or an at job. System.Clear removes a user's
// crontab and at jobs, for the kill loop. The decisions are in
// internal/guestd and internal/guestd/procs. This package only makes
// the calls: on darwin, openat, renameat and unlinkat relative to a
// descriptor of the directory, through golang.org/x/sys/unix. It never
// follows a symbolic link at a name it writes or removes.
//
// Linux and Windows guests are later (S12-platforms). Until they are
// built, every call there returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
package guestsched

// Allowed is the whole content of each allow file: root only.
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

// MaxJobEntries bounds how many entries Clear reads in the jobs
// directory (SEC13-bounded-resources). A directory with more fails the
// clear, so the kill loop reports the user as not clean.
const MaxJobEntries = 10000

// Cleared is what Clear removed.
type Cleared struct {
	// Tab is set when the user had a crontab, and it is gone.
	Tab bool
	// Jobs is how many at jobs of the user were removed.
	Jobs int
}

// Any reports whether Clear removed anything.
func (c Cleared) Any() bool { return c.Tab || c.Jobs > 0 }

// System is the guest's own cron and at.
type System struct{}
