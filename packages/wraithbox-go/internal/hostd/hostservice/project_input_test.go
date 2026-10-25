package hostservice

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	hostdv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/hostd/v1"
)

// FuzzProjectInput: the checks of a project request's key, identity, id
// and name never panic, and what they accept is in its form
// (S04-00-architecture, "Input from `wb`").
func FuzzProjectInput(f *testing.F) {
	f.Add("/r/a/.git", "https://a.example/a.git", "1111111111111111111111111111111111111111", "0123456789abcdef0123456789abcdef", "foo")
	f.Add("r/../a", "https://a b", "ABC", "", "-x")
	f.Add("/r/\x1b[2J", "sha256:00", "", "zz", "a..b")
	f.Add("/r/\u202etxt.git", "https://tok@a.example/a.git", "", "", "a\u200b")
	f.Fuzz(func(t *testing.T, path, url, root, id, name string) {
		fuzzKey(t, path)
		fuzzIdentity(t, url, root)
		if checkID(id) == nil && (len(id) != projectIDLen || strings.ToLower(id) != id) {
			t.Errorf("accepted id %q", id)
		}
		if checkName(name) == nil && (name == "" || strings.HasPrefix(name, "-") || strings.Contains(name, "..")) {
			t.Errorf("accepted name %q", name)
		}
	})
}

// fuzzKey: an accepted key path is absolute, clean, UTF-8 without a
// control character, and within its cap.
func fuzzKey(t *testing.T, path string) {
	t.Helper()
	if checkKey(&hostdv1.ProjectKey{Realm: "native", Path: path}) != nil {
		return
	}
	if len(path) > maxKeyPathLen || !utf8.ValidString(path) || hasControl(path) ||
		!filepath.IsAbs(path) || filepath.Clean(path) != path {
		t.Errorf("accepted key path %q", path)
	}
}

// fuzzIdentity: an accepted identity is deduplicated, its URL is
// printable ASCII without a space, and its root commit is a lowercase
// object ID.
func fuzzIdentity(t *testing.T, url, root string) {
	t.Helper()
	got, err := checkIdentity(&hostdv1.ProjectIdentity{Urls: []string{url, url}, RootCommits: []string{root}})
	if err != nil {
		return
	}
	outside := strings.IndexFunc(url, func(r rune) bool { return r < '!' || r > '~' }) >= 0
	userInfo := strings.ContainsAny(authority(url), "@?#")
	if len(got.URLs) != 1 || url == "" || outside || userInfo || len(url) > maxIdentityURLLen {
		t.Errorf("accepted URL %q as %q", url, got.URLs)
	}
	if !objectID(root) || strings.ToLower(root) != root {
		t.Errorf("accepted root commit %q", root)
	}
}
