package gitremote

import (
	"net/url"
	"strings"
	"testing"
)

// FuzzCleanRemoteURL feeds arbitrary remote URLs to the cleaner, with
// and without the Windows drive-letter rule. A remote URL is repository
// data, and a downloaded repository is an attacker's (S05-04-naming), so
// it gets the treatment of guest bytes. The properties:
//
//   - it never panics, and every result has a compare form;
//   - the guest URL is empty, with a reason, or `https://` with no user
//     info, query or fragment, and then it is the compare form;
//   - cleaning is idempotent on its own guest URL;
//   - removing the user info from the input doesn't change a compare
//     form that isn't a hash, so no compare form holds the password.
func FuzzCleanRemoteURL(f *testing.F) {
	for _, seed := range []string{
		"https://x-access-token:ghp_secret@github.com/org/repo.git?x=1#f",
		"ssh://git:pw@[fe80::1]:22/x",
		"git@github.com:org/repo.git",
		"git@[github.com:22]:org/repo.git",
		"user@[::1]:x",
		"ext::ssh -i key host %S 'repo'",
		"git+ssh://u:pw@h.example/x",
		"C:/x/repo",
		"https://127.1/a%2Fb",
		"/abs/path",
		"https://u:p@h@h.example/x",
		"",
	} {
		f.Add(seed, false)
		f.Add(seed, true)
	}
	f.Fuzz(func(t *testing.T, raw string, windows bool) {
		drive := noDrive
		if windows {
			drive = windowsDrive
		}
		res := clean(raw, drive)
		checkResult(t, raw, res)
		if res.Guest != "" {
			again := clean(res.Guest, drive)
			if again != res {
				t.Fatalf("clean(%q) = %+v, but cleaning its guest URL gives %+v", raw, res, again)
			}
		}
		checkUserInfoIgnored(t, raw, res, drive)
	})
}

// checkResult: a compare form always, and a guest URL only as an
// https:// URL with no user info, query or fragment, which is then the
// compare form.
func checkResult(t *testing.T, raw string, res Result) {
	t.Helper()
	if res.Compare == "" {
		t.Fatalf("clean(%q) = %+v: no compare form", raw, res)
	}
	if res.Guest == "" {
		if res.Withheld == "" {
			t.Fatalf("clean(%q) = %+v: no guest URL and no reason", raw, res)
		}
		return
	}
	if res.Withheld != "" || res.Compare != res.Guest {
		t.Fatalf("clean(%q) = %+v: a guest URL must have no reason and be the compare form", raw, res)
	}
	u, err := url.Parse(res.Guest)
	if err != nil {
		t.Fatalf("clean(%q): guest URL %q doesn't parse: %v", raw, res.Guest, err)
	}
	if !strings.HasPrefix(res.Guest, "https://") || u.User != nil || strings.ContainsAny(res.Guest, "?#") {
		t.Fatalf("clean(%q): guest URL %q is not https:// without user info, query and fragment", raw, res.Guest)
	}
}

// checkUserInfoIgnored: for an input with a scheme and user info,
// cleaning the input without the user info gives the same compare form,
// unless the compare form is a hash of the bytes.
func checkUserInfoIgnored(t *testing.T, raw string, res Result, drive func(string) bool) {
	t.Helper()
	if strings.HasPrefix(res.Compare, hashPrefix) {
		return
	}
	stripped, ok := withoutUserInfo(raw)
	if !ok {
		return
	}
	if got := clean(stripped, drive).Compare; got != res.Compare {
		t.Fatalf("clean(%q).Compare = %q, but without the user info it is %q", raw, res.Compare, got)
	}
}

// withoutUserInfo removes `userinfo@` from a URL with a scheme, where
// url.Parse finds it: in the authority, before the last `@`.
func withoutUserInfo(raw string) (string, bool) {
	scheme, ok := urlScheme(raw)
	if !ok {
		return "", false
	}
	start := len(scheme) + len("://")
	authority := raw[start:]
	if j := strings.IndexAny(authority, "/?#"); j >= 0 {
		authority = authority[:j]
	}
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return "", false
	}
	return raw[:start] + raw[start+at+1:], true
}
