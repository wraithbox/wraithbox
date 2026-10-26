package users

import (
	"errors"
	"fmt"
	"io/fs"

	gu "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestusers"
)

// home checks u's home through the volume's root, never following a
// link, and reports whether it is missing (S06-05-disks, "Owners and
// modes"; SEC08-proj-isolation). A home that another UID owns is
// refused with RuleHomeOtherOwner: making the user would hand it that
// UID's files. A home of the user's that isn't a real directory with its
// GID and mode 700 is refused with RuleHomeInvalid, and never repaired.
func (q *request) home(u User) (bool, *refusal) {
	root, r := q.volume()
	if r != nil {
		return false, r
	}
	e, err := root.Stat(u.Name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	case err != nil:
		return false, &refusal{rule: RuleHomeRoot, attr: "home", found: err.Error()}
	case e.UID != u.UID:
		return false, &refusal{rule: RuleHomeOtherOwner, attr: "home", found: describe(e)}
	case e.IsLink || !e.IsDir || e.Special || e.GID != u.GID || e.Perm != gu.HomeMode:
		return false, &refusal{rule: RuleHomeInvalid, attr: "home", found: describe(e)}
	}
	return false, nil
}

// idsFree refuses u when its UID or GID owns an entry of the volume's
// root other than its own home. A new user with that ID would own files
// it never made: another project's left behind, or guest root's
// (S06-03-image-updates, "Creation"; SEC08-proj-isolation). The root is
// read once per request, and a root over MaxRootEntries is refused.
func (q *request) idsFree(u User) *refusal {
	entries, r := q.scan()
	if r != nil {
		return r
	}
	for _, e := range entries {
		if e.Name != u.Name && (e.UID == u.UID || e.GID == u.GID) {
			return &refusal{rule: RuleIDsOwnEntry, attr: "entry " + cut(e.Name), found: describe(e)}
		}
	}
	return nil
}

// volume opens and checks the volume's root once per request.
func (q *request) volume() (gu.Root, *refusal) {
	if !q.opened {
		q.opened = true
		q.rootRule, q.rootFound = q.open()
	}
	if q.rootRule != "" {
		return nil, &refusal{rule: q.rootRule, attr: "root", found: q.rootFound}
	}
	return q.root, nil
}

// open opens the volume's root and runs the request's RootCheck on it,
// so no home is read or made on a file system other than the mount the
// caller checked (S06-05-disks, "Before every session start";
// SEC08-proj-isolation). A request without a check fails closed.
func (q *request) open() (rule, found string) {
	root, err := q.openRoot()
	if err != nil {
		return RuleHomeRoot, err.Error()
	}
	q.root = root
	if q.check == nil {
		return RuleHomeRoot, "no check of the root"
	}
	return q.check(q.ctx, root)
}

// scan reads the volume root's entries once per request.
func (q *request) scan() ([]gu.Entry, *refusal) {
	root, r := q.volume()
	if r != nil {
		return nil, r
	}
	if !q.scanned {
		q.scanned = true
		q.entries, q.scanErr = root.Entries(MaxRootEntries)
	}
	if q.scanErr != nil {
		return nil, &refusal{rule: RuleHomeRoot, attr: "root entries", found: q.scanErr.Error()}
	}
	return q.entries, nil
}

// describe is an entry for the log.
func describe(e gu.Entry) string {
	return fmt.Sprintf("dir=%t link=%t uid=%d gid=%d mode=%04o special=%t",
		e.IsDir, e.IsLink, e.UID, e.GID, uint32(e.Perm), e.Special)
}
