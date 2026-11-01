// Package projectuser holds what wb-hostd and wb-guestd share about a
// project user: the range of its IDs, the form of its names, its home
// path and its GeneratedUIDs (S06-03-image-updates, "Host state per
// project user", "Allocation"). wb-hostd allocates every value. wb-guestd
// only checks them.
package projectuser

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
)

// The ID range of project users. wb-hostd hands out each number once, as
// both a UID and a GID, from FirstID up, and refuses past LastID with
// RuleIDsExhausted (S06-03-image-updates, "Allocation"). The image uses
// no ID in the range. LastID is below 2^31, so a project user's ID is
// never negative as an int32 and stays below macOS's `nobody`
// (4294967294, -2). wb-guestd's kill loop stops processes of the IDs in
// this range.
const (
	FirstID uint32 = 10000
	LastID  uint32 = 999_999_999
)

// RuleIDsExhausted: wb-hostd's counter is past LastID, so no new project
// user can be made on this host.
const RuleIDsExhausted = "project-user-ids-exhausted"

// HomeRoot is the data volume's mount point in a macOS guest, which
// holds every home (S06-05-disks, "Mounting the data volume"). It equals
// internal/guestd's HomePath.
const HomeRoot = "/private/var/wbhomes"

// namePrefix starts every project user's name.
const namePrefix = "wbp"

// MaxNameLen is the longest user name: the prefix and the ten digits of
// a uint32.
const MaxNameLen = len(namePrefix) + 10

// UserName is the name of the user with uid, which is also the name of
// its own group. A name made from the UID is unique for as long as the
// UID is, and needs no escaping.
func UserName(uid uint32) string { return namePrefix + strconv.FormatUint(uint64(uid), 10) }

// IsUserName reports whether name is the name UserName gives an ID in
// the range of project users. It accepts each such name in one form
// only: no sign, no leading zero.
func IsUserName(name string) bool {
	digits, ok := strings.CutPrefix(name, namePrefix)
	if !ok {
		return false
	}
	id, err := strconv.ParseUint(digits, 10, 32)
	return err == nil && InRange(uint32(id)) && UserName(uint32(id)) == name
}

// HomePath is the home of the user named name.
func HomePath(name string) string { return HomeRoot + "/" + name }

// InRange reports whether id is in the range of project user IDs.
func InRange(id uint32) bool { return id >= FirstID && id <= LastID }

// NewGeneratedUID returns a GeneratedUID: 128 bits from crypto/rand, as
// an uppercase version 4 UUID, the form macOS writes.
func NewGeneratedUID() string {
	var b [16]byte
	// crypto/rand.Read never fails: it crashes the program instead.
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// ValidGeneratedUID reports whether s is in the form NewGeneratedUID
// makes: 36 bytes of uppercase hexadecimal digits and hyphens, version 4,
// RFC 9562 variant.
func ValidGeneratedUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return s[14] == '4' && strings.IndexByte("89AB", s[19]) >= 0
}

// ProjectIDLen is the length of a project id: 128 bits in hexadecimal
// (S05-04-naming, "Project id").
const ProjectIDLen = 32

// ValidProjectID reports whether id is 32 lowercase hexadecimal digits,
// the form of a project id (S05-04-naming, "Project id").
func ValidProjectID(id string) bool {
	if len(id) != ProjectIDLen {
		return false
	}
	for i := range len(id) {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
