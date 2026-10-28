// Package guestusers is the guest's side of the project users: it reads
// and writes user and group records in the guest's local directory, and
// checks and makes homes under the data volume's mount root
// (S06-03-image-updates, "Creation"; S06-05-disks, "Owners and modes").
// wb-guestd runs it as root in the guest. The decisions are in
// internal/guestd/users. This package only makes the calls: on darwin,
// /usr/bin/dscl with an argument list, never a shell, and fstatat,
// mkdirat, fchown and fchmod relative to a descriptor of the mount root.
//
// Linux and Windows guests are later (S12-platforms). Until they are
// built, every call there returns an error that wraps
// errors.ErrUnsupported (S12-01-platform-interfaces, "Unimplemented
// platforms fail clearly").
package guestusers

import (
	"context"
	"errors"
	"io/fs"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/guestmount"
)

// Kind is the kind of a directory record.
type Kind int

// The kinds of record the project users have.
const (
	Users Kind = iota + 1
	Groups
)

func (k Kind) String() string {
	switch k {
	case Users:
		return "user"
	case Groups:
		return "group"
	}
	return "unknown"
}

// The attributes of a record that wb-guestd reads or writes, by their
// standard names.
const (
	AttrRecordName     = "RecordName"
	AttrGeneratedUID   = "GeneratedUID"
	AttrUniqueID       = "UniqueID"
	AttrPrimaryGroupID = "PrimaryGroupID"
	AttrHome           = "NFSHomeDirectory"
	AttrShell          = "UserShell"
	AttrRealName       = "RealName"
	AttrPassword       = "Password"
	// AttrIsHidden hides a user from the login window. dscl has no
	// standard name for it, so it writes the attribute as a native one
	// and reports it under KeyIsHidden (RecordKey).
	AttrIsHidden = "IsHidden"
	// AttrAuthAuthority holds a user's password hash reference or a
	// disabled marker. A project user has none.
	AttrAuthAuthority = "AuthenticationAuthority"
	// AttrGroupMembership and AttrGroupMembers list a group's members
	// beyond those with it as their primary group, by name and by
	// GeneratedUID. A project user is a member of no group.
	AttrGroupMembership = "GroupMembership"
	AttrGroupMembers    = "GroupMembers"
	// AttrNestedGroups lists the groups, by GeneratedUID, whose members
	// are members of this group too. A project group nests none and is
	// nested in none.
	AttrNestedGroups = "NestedGroups"
	// AttrShadowHash is a user's password hash. dscl reports it under its
	// native name. A project user has none.
	AttrShadowHash = "dsAttrTypeNative:ShadowHashData"
)

// NativePrefix starts the name of each native attribute in dscl's
// -plist form: one that dscl has no standard name for.
const NativePrefix = "dsAttrTypeNative:"

// KeyIsHidden is the key of AttrIsHidden in a Record.
const KeyIsHidden = NativePrefix + AttrIsHidden

// RecordKey is the key under which a Record holds the attribute that
// Directory.Create wrote as attr. It is attr for a standard attribute,
// and the native name for one that dscl has no standard name for. A
// read-back compares by this key, so it finds what was written.
func RecordKey(attr string) string {
	if attr == AttrIsHidden {
		return KeyIsHidden
	}
	return attr
}

// HomeMode is the mode of every home (S06-05-disks, "Owners and modes").
const HomeMode fs.FileMode = 0o700

// Record is one directory record: the values of each attribute, by the
// attribute's standard name. An attribute outside the standard set keeps
// its full name, such as dsAttrTypeNative:x.
type Record map[string][]string

// Directory reads and writes the guest's local user and group records.
// *System implements it on darwin. Tests pass a fake.
type Directory interface {
	// List returns every record of kind, each with only the attributes
	// attrs that it has.
	List(ctx context.Context, kind Kind, attrs []string) ([]Record, error)
	// Read returns the record of kind with name among its names, with
	// every attribute, and found false when there is none.
	Read(ctx context.Context, kind Kind, name string) (rec Record, found bool, err error)
	// Create sets attr of the record of kind named name to the one value
	// value, and makes the record first when it is missing. Read and
	// List report it under RecordKey(attr).
	Create(ctx context.Context, kind Kind, name, attr, value string) error
}

// Entry is what fstatat reports of an entry of the mount root, never
// following a link.
type Entry struct {
	Name   string
	IsDir  bool
	IsLink bool
	UID    uint32
	GID    uint32
	// Perm is the permission bits, without the type, setuid, setgid and
	// sticky bits.
	Perm fs.FileMode
	// Special is set when the setuid, setgid or sticky bit is set.
	Special bool
}

// Root is an open descriptor of the data volume's mount root. Every call
// names an entry of the root itself, one path component, and never
// follows a link (S06-05-disks, "Owners and modes").
type Root interface {
	// Stat reports the entry name, and an error that wraps
	// fs.ErrNotExist when it is missing.
	Stat(name string) (Entry, error)
	// Mkdir makes the directory name with mode 700, then gives it to uid
	// and gid with fchown and fchmod on a descriptor opened with
	// O_NOFOLLOW. It fails when name exists.
	Mkdir(name string, uid, gid uint32) error
	// Entries reports every entry of the root, and an error that wraps
	// ErrTooManyEntries when there are more than limit.
	Entries(limit int) ([]Entry, error)
	// Mount reports the file system that holds the root, from fstatfs on
	// its descriptor, so the caller can check it is the mount it checked
	// by path.
	Mount() (guestmount.Mount, error)
	Close() error
}

// ErrTooManyEntries is the error of Root.Entries for a root with more
// entries than the limit. Guest root can fill the volume root, so the
// scan stops there.
var ErrTooManyEntries = errors.New("guestusers: the mount root has more entries than the scan takes")
