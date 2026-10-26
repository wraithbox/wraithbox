package users

import (
	"fmt"
	"slices"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/projectuser"
)

// CheckRequest checks the request as a whole: one to MaxPerRequest
// users, and no UID, user name or GeneratedUID in two users
// (proto/wraithbox/guest/v1, EnsureUsersRequest). An error is refused
// with RuleRequest. What concerns one user alone, a GeneratedUID of the
// wrong form or the same one for its user and group, is left to
// validate, so that user is refused with RuleRequestInvalid and the
// others go on.
func CheckRequest(us []User) error {
	if len(us) == 0 || len(us) > MaxPerRequest {
		return fmt.Errorf("%d users, want 1 to %d", len(us), MaxPerRequest)
	}
	byUID := make(map[uint32]bool, len(us))
	names := make(map[string]bool, len(us))
	byGUID := make(map[string]bool, 2*len(us))
	for _, u := range us {
		if byUID[u.UID] || names[u.Name] {
			return fmt.Errorf("the UID %d or the name %s twice", u.UID, cut(u.Name))
		}
		byUID[u.UID], names[u.Name] = true, true
		own := ownGUIDs(u)
		for _, g := range own {
			if byGUID[g] {
				return fmt.Errorf("the GeneratedUID %s in two users", cut(g))
			}
		}
		for _, g := range own {
			byGUID[g] = true
		}
	}
	return nil
}

// ownGUIDs returns u's distinct GeneratedUIDs of the valid form.
func ownGUIDs(u User) []string {
	var out []string
	for _, g := range []string{u.UserGUID, u.GroupGUID} {
		if projectuser.ValidGeneratedUID(g) && !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// validate checks one user's values against the form wb-hostd
// allocates (S06-03-image-updates, "Allocation"): an ID in the project
// range, used as both UID and GID, the name made from it and equal to
// the group's, two distinct GeneratedUIDs and the home under the mount
// point. A user that fails is refused with RuleRequestInvalid, and
// nothing is read or written for it.
func validate(u User) *refusal {
	field := ""
	switch {
	case !projectID(u.ProjectID):
		field = "project_id"
	case !projectuser.InRange(u.UID):
		field = "uid"
	case u.GID != u.UID:
		field = "gid"
	case u.Name != projectuser.UserName(u.UID):
		field = "user_name"
	case u.GroupName != u.Name:
		field = "group_name"
	case !projectuser.ValidGeneratedUID(u.UserGUID):
		field = "user_generated_uid"
	case !projectuser.ValidGeneratedUID(u.GroupGUID) || u.GroupGUID == u.UserGUID:
		field = "group_generated_uid"
	case u.Home != projectuser.HomePath(u.Name):
		field = "home_path"
	default:
		return nil
	}
	return &refusal{rule: RuleRequestInvalid, attr: field, found: "not in the form wb-hostd allocates"}
}

// projectIDLen is the length of a project id: 128 bits in hexadecimal
// (S05-04-naming, "Project id").
const projectIDLen = 32

// projectID reports whether id is 32 lowercase hexadecimal digits.
func projectID(id string) bool {
	if len(id) != projectIDLen {
		return false
	}
	for i := range len(id) {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
