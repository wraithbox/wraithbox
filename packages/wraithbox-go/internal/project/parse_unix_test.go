//go:build unix

package project

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestParseRevParse(t *testing.T) {
	common, gitDir, err := parseRevParse([]byte("/r/.git\n/r/.git/worktrees/w\n"))
	if err != nil || common != "/r/.git" || gitDir != "/r/.git/worktrees/w" {
		t.Errorf("parseRevParse = %q, %q, %v", common, gitDir, err)
	}
	for in, want := range map[string]error{
		"/r/.git\n/r/.git": errNoEnd,
		"/r/.git\n":        errNotTwoLines,
		"/a\n/b\n/c\n":     errNotTwoLines,
		"/a\nrel\n":        errRelative,
		"\n/b\n":           errEmptyPath,
		"/a\x1b[2J\n/b\n":  errControl,
		"/a\r\n/b\n":       errControl,
		"/\xff\n/b\n":      errControl,
		"/" + strings.Repeat("a", 4096) + "\n/b\n": errLongPath,
	} {
		if _, _, err := parseRevParse([]byte(in)); !errors.Is(err, want) {
			t.Errorf("parseRevParse(%q) = %v, want %v", in, err, want)
		}
	}
}

// TestParseBackLinkAndWorktree: a relative path is joined to the git
// directory without cleaning, so `..` is resolved on disk after links.
func TestParseBackLinkAndWorktree(t *testing.T) {
	const gitDir = "/r/.git/worktrees/w"
	for in, want := range map[string]string{
		"/wt/.git\n":            "/wt/.git",
		"../../../../wt/.git\n": gitDir + "/../../../../wt/.git",
	} {
		if got, err := parseBackLink([]byte(in), gitDir); err != nil || got != want {
			t.Errorf("parseBackLink(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "/wt/.git", "\n", "/wt\n/x\n", "/wt\x00\n"} {
		if got, err := parseBackLink([]byte(in), gitDir); err == nil {
			t.Errorf("parseBackLink(%q) = %q, want an error", in, got)
		}
	}
	if got, err := parseWorktreeValue([]byte("../../../sub\x00"), "/p/.git/modules/sub"); err != nil || got != "/p/.git/modules/sub/../../../sub" {
		t.Errorf("parseWorktreeValue = %q, %v", got, err)
	}
	for _, in := range []string{"", "/x", "\x00", "/x\x00/y\x00", "/x\ny\x00"} {
		if got, err := parseWorktreeValue([]byte(in), "/p"); err == nil {
			t.Errorf("parseWorktreeValue(%q) = %q, want an error", in, got)
		}
	}
}

func TestParseRemoteNames(t *testing.T) {
	lim := limits{remotes: 3, remoteName: 8}
	names, ref := parseRemoteNames([]byte("origin\nup\n"), lim)
	if ref != nil || !slices.Equal(names, []string{"origin", "up"}) {
		t.Errorf("parseRemoteNames = %q, %v", names, ref)
	}
	if names, ref := parseRemoteNames(nil, lim); ref != nil || names != nil {
		t.Errorf("parseRemoteNames(empty) = %q, %v", names, ref)
	}
	for in, rule := range map[string]Rule{
		"a\nb\nc\nd\n": RuleTooManyRemotes,
		"-x\n":         RuleRemoteName,
		"a\tb\n":       RuleRemoteName,
		"\n":           RuleRemoteName,
		"remote-12\n":  RuleRemoteName,
		"origin":       RuleGitOutput,
	} {
		if _, ref := parseRemoteNames([]byte(in), lim); ref == nil || ref.Rule != rule {
			t.Errorf("parseRemoteNames(%q) = %v, want %s", in, ref, rule)
		}
	}
}

// TestParseURLsNeverQuotesURL: a refusal message names the remote and
// the URL's number, never the URL, which can hold a token.
func TestParseURLsNeverQuotesURL(t *testing.T) {
	lim := limits{urls: 1, urlLen: 30}
	for in, rule := range map[string]Rule{
		"https://u:ghp_secret@h.example/x\n":          RuleURLTooLong,
		"https://a.example\nhttps://u:ghp_secret@b\n": RuleTooManyURLs,
		"https://u:ghp_secret@h":                      RuleGitOutput,
	} {
		_, ref := parseURLs([]byte(in), "origin", lim)
		if ref == nil || ref.Rule != rule {
			t.Fatalf("parseURLs(%q) = %v, want %s", in, ref, rule)
		}
		if strings.Contains(ref.Error(), "ghp_secret") {
			t.Errorf("parseURLs refusal %q quotes the URL", ref)
		}
	}
}

func TestParseRootCommits(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 64)
	roots, ref := parseRootCommits([]byte(b+"\n"+a+"\n"+a+"\n"), limits{roots: 3})
	if ref != nil || !slices.Equal(roots, []string{a, b}) {
		t.Errorf("parseRootCommits = %q, %v", roots, ref)
	}
	for in, rule := range map[string]Rule{
		a + "\n" + a + "\n" + a + "\n" + a + "\n": RuleTooManyRoots,
		strings.Repeat("A", 40) + "\n":            RuleRootCommitForm,
		strings.Repeat("a", 41) + "\n":            RuleRootCommitForm,
		"\n":                                      RuleRootCommitForm,
		a:                                         RuleGitOutput,
	} {
		if _, ref := parseRootCommits([]byte(in), limits{roots: 3}); ref == nil || ref.Rule != rule {
			t.Errorf("parseRootCommits(%q) = %v, want %s", in, ref, rule)
		}
	}
}
