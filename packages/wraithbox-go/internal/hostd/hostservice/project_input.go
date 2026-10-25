package hostservice

import (
	"fmt"
	"path/filepath"
	"unicode"
	"unicode/utf8"

	"google.golang.org/grpc/codes"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/hostd/state"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/project"
)

// The caps on a project request from wb (S04-08-records-and-bounds,
// "Input from `wb`"). wb is a process of the user, but the repository it
// reads is not, so every field is checked here as untrusted input
// (SEC13-bounded-resources). A request over a cap is refused with
// RuleProjectOverCap, never cut.
const (
	// maxKeyPathLen is the longest key path, Linux's PATH_MAX, as
	// internal/project reads it.
	maxKeyPathLen = 4096
	// maxIdentityURLs is wb's own cap: 64 remotes of 16 URLs each.
	maxIdentityURLs = 1024
	// maxIdentityURLLen leaves room for the escaping of wb's cap of 2048
	// bytes per URL in a URL's compare form.
	maxIdentityURLLen = 8192
	// maxIdentityURLBytes bounds all URLs of one identity together.
	maxIdentityURLBytes = 1 << 20
	// maxIdentityRoots is wb's own cap on root commits.
	maxIdentityRoots = 1024
)

// MaxMessageSize is the largest request and response wb-hostd's gRPC
// server takes and sends. An identity at the caps above is about
// 1.1 MiB.
const MaxMessageSize = 4 << 20

// projectIDLen is the length of a project id: 128 bits in lowercase
// hexadecimal (S05-04-naming, "Project id").
const projectIDLen = 32

func overCap(what string, n, limit int) *projectRefusal {
	return &projectRefusal{
		code: codes.InvalidArgument, rule: RuleProjectOverCap,
		msg: fmt.Sprintf("the request's %s is %d, over wb-hostd's cap of %d", what, n, limit),
	}
}

func badForm(format string, args ...any) *projectRefusal {
	return &projectRefusal{code: codes.InvalidArgument, rule: RuleProjectForm, msg: fmt.Sprintf(format, args...)}
}

// checkKey checks a key: the native realm, and an absolute, clean path
// of UTF-8 without a control character. wb-hostd never opens the path.
// It is only a key, but it is printed and logged.
func checkKey(k *hostdv1.ProjectKey) error {
	if k == nil {
		return badForm("the request has no project key")
	}
	if k.GetRealm() != string(project.RealmNative) {
		return &projectRefusal{
			code: codes.InvalidArgument, rule: RuleProjectRealm,
			msg: fmt.Sprintf("the realm %q isn't supported: only %s is", k.GetRealm(), project.RealmNative),
		}
	}
	p := k.GetPath()
	switch {
	case len(p) > maxKeyPathLen:
		return overCap("key path length", len(p), maxKeyPathLen)
	case p == "" || !utf8.ValidString(p) || hasControl(p):
		return badForm("the key path is empty, not UTF-8, or holds a control character")
	case !filepath.IsAbs(p) || filepath.Clean(p) != p:
		return badForm("the key path %q isn't an absolute path in clean form", p)
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// checkIdentity checks an identity and returns it sorted and without
// duplicates. A missing identity is an empty one: a repository with
// neither a remote nor a commit.
func checkIdentity(id *hostdv1.ProjectIdentity) (project.Identity, error) {
	urls, roots := id.GetUrls(), id.GetRootCommits()
	if len(urls) > maxIdentityURLs {
		return project.Identity{}, overCap("URL count", len(urls), maxIdentityURLs)
	}
	if len(roots) > maxIdentityRoots {
		return project.Identity{}, overCap("root commit count", len(roots), maxIdentityRoots)
	}
	total := 0
	for i, u := range urls {
		total += len(u)
		switch {
		case len(u) > maxIdentityURLLen:
			return project.Identity{}, overCap(fmt.Sprintf("URL %d length", i+1), len(u), maxIdentityURLLen)
		case total > maxIdentityURLBytes:
			return project.Identity{}, overCap("URL bytes", total, maxIdentityURLBytes)
		case !compareForm(u):
			return project.Identity{}, badForm("URL %d isn't a compare form: 1 or more bytes of printable ASCII without a space", i+1)
		}
	}
	for i, r := range roots {
		if !objectID(r) {
			return project.Identity{}, badForm("root commit %d isn't a 40- or 64-character lowercase object ID", i+1)
		}
	}
	return project.Identity{URLs: urls, RootCommits: roots}.Normalized(), nil
}

// compareForm accepts what gitremote.Result.Compare can be: printable
// ASCII without a space.
func compareForm(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}

// objectID accepts a SHA-1 or SHA-256 object ID in lowercase hex.
func objectID(s string) bool {
	return (len(s) == 40 || len(s) == 64) && lowerHex(s)
}

func lowerHex(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// checkID checks a project id: 32 lowercase hex characters.
func checkID(id string) error {
	if len(id) != projectIDLen || !lowerHex(id) {
		return &projectRefusal{
			code: codes.InvalidArgument, rule: RuleProjectIDForm,
			msg: fmt.Sprintf("the project id %q isn't %d lowercase hexadecimal characters", truncate(id), projectIDLen),
		}
	}
	return nil
}

// checkName checks a project name with the rules of wb project rename.
func checkName(name string) error {
	if err := project.ValidName(name); err != nil {
		return &projectRefusal{code: codes.InvalidArgument, rule: RuleProjectNameInvalid, msg: err.Error()}
	}
	return nil
}

// placementOf maps the contract's placement. unspecified is what
// PLACEMENT_UNSPECIFIED gives, and "" refuses it. A value the enum
// doesn't have is refused.
func placementOf(p hostdv1.Placement, unspecified string) (string, error) {
	switch p {
	case hostdv1.Placement_PLACEMENT_WORK:
		return state.PlacementWork, nil
	case hostdv1.Placement_PLACEMENT_ISOLATED:
		return state.PlacementIsolated, nil
	case hostdv1.Placement_PLACEMENT_UNSPECIFIED:
		if unspecified != "" {
			return unspecified, nil
		}
	}
	return "", &projectRefusal{
		code: codes.InvalidArgument, rule: RuleProjectPlacement,
		msg: "the request names no placement: name work or isolated",
	}
}

// truncate shortens a value a message quotes back. The message is for
// the user, and a request can hold any bytes.
func truncate(s string) string {
	const limit = 80
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}
