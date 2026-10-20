package project

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The parsers here read bytes a repository controls: git's output about
// it, and files in its git directory. Each is fuzzed (fuzz_test.go).

// maxPathLen bounds a path read from git or a git file. It is Linux's
// PATH_MAX, above macOS's 1024.
const maxPathLen = 4096

// maxQuoted bounds the part of a name or line that a message quotes.
const maxQuoted = 80

var (
	errEmptyPath   = errors.New("is empty")
	errLongPath    = fmt.Errorf("is longer than %d bytes", maxPathLen)
	errControl     = errors.New("holds a control character or isn't UTF-8")
	errRelative    = errors.New("is not an absolute path")
	errNotTwoLines = errors.New("is not two lines")
	errNoEnd       = errors.New("doesn't end as git ends it")
)

// checkPath accepts a path of 1 to maxPathLen bytes of UTF-8 without a
// control character.
func checkPath(p string) error {
	switch {
	case p == "":
		return errEmptyPath
	case len(p) > maxPathLen:
		return errLongPath
	case hasControl(p):
		return errControl
	}
	return nil
}

// parseRevParse reads the output of `git rev-parse --path-format=absolute
// --git-common-dir --git-dir`: two lines, each an absolute path.
func parseRevParse(out []byte) (common, gitDir string, err error) {
	s, ok := strings.CutSuffix(string(out), "\n")
	if !ok {
		return "", "", errNoEnd
	}
	lines := strings.Split(s, "\n")
	if len(lines) != 2 {
		return "", "", errNotTwoLines
	}
	for _, p := range lines {
		if err := checkPath(p); err != nil {
			return "", "", err
		}
		if !filepath.IsAbs(p) {
			return "", "", errRelative
		}
	}
	return lines[0], lines[1], nil
}

// parseBackLink reads a linked worktree's back-link file,
// `<common>/worktrees/<name>/gitdir`: the path of the worktree's `.git`
// file and a line end. A relative path is relative to gitDir, the
// directory that holds the file, as git writes it with
// worktree.useRelativePaths.
func parseBackLink(content []byte, gitDir string) (string, error) {
	s, ok := strings.CutSuffix(string(content), "\n")
	if !ok {
		return "", errNoEnd
	}
	return underGitDir(s, gitDir)
}

// parseWorktreeValue reads the output of `git config --null --get
// core.worktree`: the value and a NUL. A relative value is relative to
// gitDir, as git reads it.
func parseWorktreeValue(out []byte, gitDir string) (string, error) {
	s, ok := strings.CutSuffix(string(out), "\x00")
	if !ok {
		return "", errNoEnd
	}
	return underGitDir(s, gitDir)
}

// underGitDir checks a path and makes a relative one relative to gitDir.
// It joins without cleaning: CanonicalDir then resolves `..` after the
// links before it, as git does, where filepath.Clean would drop them as
// text.
func underGitDir(p, gitDir string) (string, error) {
	if err := checkPath(p); err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		p = gitDir + string(filepath.Separator) + p
	}
	if len(p) > maxPathLen {
		return "", errLongPath
	}
	return p, nil
}

// limits are the caps on a repository's identity
// (SEC13-bounded-resources, S04-08-records-and-bounds). A downloaded
// repository is an attacker's, so its data is bounded as guest input
// is. Over a cap the identity is refused, never cut.
type limits struct {
	remotes    int // remotes per repository
	remoteName int // bytes per remote name
	urls       int // URLs per remote
	urlLen     int // bytes per URL
	roots      int // root commits per repository
}

var defaultLimits = limits{
	remotes:    64,
	remoteName: 256,
	urls:       16,
	urlLen:     2048,
	roots:      1024,
}

// lines splits output that ends in a line end, or is empty, into its
// lines.
func lines(out []byte) ([]string, bool) {
	if len(out) == 0 {
		return nil, true
	}
	s, ok := strings.CutSuffix(string(out), "\n")
	if !ok {
		return nil, false
	}
	return strings.Split(s, "\n"), true
}

// quoted quotes the start of s for a message.
func quoted(s string) string {
	if len(s) > maxQuoted {
		s = s[:maxQuoted]
	}
	return strconv.Quote(s)
}

// parseRemoteNames reads the output of `git remote`: one name a line.
// Each name is passed to git as an argument, so a name that starts with
// `-` is refused, as is an empty or overlong one, or one with a control
// character.
func parseRemoteNames(out []byte, lim limits) ([]string, *Refusal) {
	names, ok := lines(out)
	if !ok {
		return nil, &Refusal{Rule: RuleGitOutput, msg: "git remote printed a list that doesn't end in a line end"}
	}
	if len(names) > lim.remotes {
		return nil, &Refusal{
			Rule: RuleTooManyRemotes,
			msg:  fmt.Sprintf("the repository has %d remotes, more than the %d Wraith Box reads", len(names), lim.remotes),
		}
	}
	for _, name := range names {
		var why string
		switch {
		case name == "":
			why = "is empty"
		case len(name) > lim.remoteName:
			why = fmt.Sprintf("is longer than %d bytes", lim.remoteName)
		case strings.HasPrefix(name, "-"):
			why = "starts with -, so git would read it as an option"
		case hasControl(name):
			why = "holds a control character or isn't UTF-8"
		default:
			continue
		}
		return nil, &Refusal{Rule: RuleRemoteName, msg: fmt.Sprintf("the remote name %s %s. Rename the remote", quoted(name), why)}
	}
	return names, nil
}

// parseURLs reads the output of `git remote get-url --all <name>`: one
// URL a line. A message never quotes a URL: it can hold a token.
func parseURLs(out []byte, name string, lim limits) ([]string, *Refusal) {
	urls, ok := lines(out)
	if !ok {
		return nil, &Refusal{
			Rule: RuleGitOutput,
			msg:  fmt.Sprintf("git remote get-url printed URLs of remote %s that don't end in a line end", quoted(name)),
		}
	}
	if len(urls) > lim.urls {
		return nil, &Refusal{
			Rule: RuleTooManyURLs,
			msg:  fmt.Sprintf("the remote %s has %d URLs, more than the %d Wraith Box reads", quoted(name), len(urls), lim.urls),
		}
	}
	for i, u := range urls {
		if len(u) > lim.urlLen {
			return nil, &Refusal{
				Rule: RuleURLTooLong,
				msg:  fmt.Sprintf("URL %d of the remote %s is %d bytes, longer than the %d Wraith Box reads", i+1, quoted(name), len(u), lim.urlLen),
			}
		}
	}
	return urls, nil
}

// parseRootCommits reads the output of `git rev-list --max-parents=0
// --all`: one object ID a line, 40 or 64 lowercase hexadecimal
// characters. It returns them sorted, without duplicates.
func parseRootCommits(out []byte, lim limits) ([]string, *Refusal) {
	ids, ok := lines(out)
	if !ok {
		return nil, &Refusal{Rule: RuleGitOutput, msg: "git rev-list printed root commits that don't end in a line end"}
	}
	if len(ids) > lim.roots {
		return nil, &Refusal{
			Rule: RuleTooManyRoots,
			msg:  fmt.Sprintf("the repository has %d root commits, more than the %d Wraith Box records. Add a remote", len(ids), lim.roots),
		}
	}
	for _, id := range ids {
		if !isObjectID(id) {
			return nil, &Refusal{Rule: RuleRootCommitForm, msg: fmt.Sprintf("git rev-list printed %s, which is not an object ID", quoted(id))}
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// isObjectID accepts a SHA-1 or SHA-256 object ID in lowercase
// hexadecimal.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return !strings.ContainsFunc(s, func(c rune) bool {
		return (c < '0' || c > '9') && (c < 'a' || c > 'f')
	})
}
