package project

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// The parsers here read bytes a repository controls. A downloaded
// archive is an attacker's (S05-04-naming), so they are fuzzed as a
// parser of guest bytes is.

// FuzzGitPaths feeds arbitrary bytes to the parsers of git's paths: the
// rev-parse output, the back-link file and the core.worktree value. An
// accepted path is 1 to maxPathLen bytes of UTF-8 without a control
// character, and absolute: rev-parse's as printed, the others after
// joining with an absolute git directory.
func FuzzGitPaths(f *testing.F) {
	for _, seed := range []string{
		"/r/.git\n/r/.git/worktrees/w\n",
		"/r/.git\n",
		"/a\x1b[2J\n/b\n",
		"../../../../wt/.git\n",
		"../../../sub\x00",
		"C:/x\nC:/y\n",
		"",
	} {
		f.Add([]byte(seed))
	}
	gitDir, err := filepath.Abs(filepath.Join("r", ".git", "worktrees", "w"))
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, out []byte) {
		if common, dir, err := parseRevParse(out); err == nil {
			requireGoodPath(t, "parseRevParse", out, common)
			requireGoodPath(t, "parseRevParse", out, dir)
		}
		if p, err := parseBackLink(out, gitDir); err == nil {
			requireGoodPath(t, "parseBackLink", out, p)
		}
		if p, err := parseWorktreeValue(out, gitDir); err == nil {
			requireGoodPath(t, "parseWorktreeValue", out, p)
		}
	})
}

func requireGoodPath(t *testing.T, parser string, in []byte, p string) {
	t.Helper()
	if checkPath(p) != nil || !filepath.IsAbs(p) {
		t.Fatalf("%s(%q) accepted %q", parser, in, p)
	}
}

// FuzzIdentityOutput feeds arbitrary bytes to the parsers of `git
// remote`, `git remote get-url --all` and `git rev-list`. What they
// accept is within the caps: names that don't start with `-` and have
// no control character, URLs within their length, and root commits that
// are object IDs, sorted and unique. A refusal names a rule and never
// quotes a URL.
func FuzzIdentityOutput(f *testing.F) {
	lim := limits{remotes: 4, remoteName: 16, urls: 4, urlLen: 64, roots: 4}
	for _, seed := range []string{
		"origin\nup\n",
		"-x\n",
		"https://u:tokentoken@h.example/x\nhttps://h.example/y\n",
		strings.Repeat("a", 40) + "\n" + strings.Repeat("b", 64) + "\n",
		"",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, out []byte) {
		if names, ref := parseRemoteNames(out, lim); ref == nil {
			checkNames(t, out, names, lim)
		} else if ref.Rule == "" {
			t.Fatalf("parseRemoteNames(%q): refusal without a rule", out)
		}
		urls, ref := parseURLs(out, "origin", lim)
		if ref == nil {
			checkURLs(t, out, urls, lim)
		} else {
			checkURLRefusal(t, out, ref)
		}
		if roots, ref := parseRootCommits(out, lim); ref == nil {
			checkRoots(t, out, roots, lim)
		} else if ref.Rule == "" {
			t.Fatalf("parseRootCommits(%q): refusal without a rule", out)
		}
	})
}

func checkNames(t *testing.T, out []byte, names []string, lim limits) {
	t.Helper()
	if len(names) > lim.remotes {
		t.Fatalf("parseRemoteNames(%q) accepted %d names", out, len(names))
	}
	for _, n := range names {
		if n == "" || len(n) > lim.remoteName || strings.HasPrefix(n, "-") || hasControl(n) {
			t.Fatalf("parseRemoteNames(%q) accepted %q", out, n)
		}
	}
}

func checkURLs(t *testing.T, out []byte, urls []string, lim limits) {
	t.Helper()
	if len(urls) > lim.urls {
		t.Fatalf("parseURLs(%q) accepted %d URLs", out, len(urls))
	}
	for _, u := range urls {
		if len(u) > lim.urlLen {
			t.Fatalf("parseURLs(%q) accepted %d bytes", out, len(u))
		}
	}
}

// checkURLRefusal: the message never holds a line of the input longer
// than the remote name, which it quotes.
func checkURLRefusal(t *testing.T, out []byte, ref *Refusal) {
	t.Helper()
	if ref.Rule == "" {
		t.Fatalf("parseURLs(%q): refusal without a rule", out)
	}
	for line := range bytes.SplitSeq(out, []byte("\n")) {
		if len(line) > len("origin") && strings.Contains(ref.Error(), string(line)) {
			t.Fatalf("parseURLs(%q): refusal %q quotes a URL", out, ref)
		}
	}
}

func checkRoots(t *testing.T, out []byte, roots []string, lim limits) {
	t.Helper()
	if len(roots) > lim.roots {
		t.Fatalf("parseRootCommits(%q) accepted %d roots", out, len(roots))
	}
	for i, id := range roots {
		if !isObjectID(id) || i > 0 && roots[i-1] >= id {
			t.Fatalf("parseRootCommits(%q) = %q: not sorted, unique object IDs", out, roots)
		}
	}
}

// FuzzNameFrom: any directory name gives a valid project name.
func FuzzNameFrom(f *testing.F) {
	for _, seed := range []string{"foo", "a..b", ".x", "日本", "", strings.Repeat(".", 100)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if name := nameFrom(s); ValidName(name) != nil {
			t.Fatalf("nameFrom(%q) = %q, which ValidName refuses: %v", s, name, ValidName(name))
		}
	})
}
