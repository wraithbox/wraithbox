package project

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The parsers here read bytes a repository controls. A downloaded
// archive is an attacker's (S05-04-naming), so they are fuzzed as a
// parser of guest bytes is.

// FuzzGitPaths feeds arbitrary bytes to the parsers of git's paths: the
// rev-parse output, the back-link file and the core.worktree value of a
// main checkout, linked worktree or submodule, from config or
// config.worktree. An accepted path is 1 to maxPathLen bytes of UTF-8
// without a control character, and absolute: rev-parse's as printed, the
// others after
// joining with an absolute git directory.
func FuzzGitPaths(f *testing.F) {
	for _, seed := range []string{
		"/r/.git\n/r/.git/worktrees/w\n",
		"/r/.git\n",
		"/a\x1b[2J\n/b\n",
		"../../../../wt/.git\n",
		"../../../sub\x00",
		"/other/project\x00",
		"..\x00",
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
		if urls, ref := parseURLs(out, "origin", lim); ref == nil {
			checkURLs(t, out, urls, lim)
		} else if problem := urlRefusalProblem(parseURLs, out, lim); problem != "" {
			t.Fatal(problem)
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

// urlParser is the signature of parseURLs, so a test can hand
// urlRefusalProblem a broken parser.
type urlParser func(out []byte, name string, lim limits) ([]string, *Refusal)

// urlRefusalProblem says how parse's refusal of out breaks the rule that
// a refusal names a rule and never quotes a URL (SEC04-no-guest-secrets:
// a URL can hold a token), or returns "" when it keeps it. A refusal
// depends on the number and lengths of the lines alone, so the input is
// parsed again with every byte but the line ends replaced, once by 'x'
// and once by 'y'. A message that changes quoted a URL. The second mask
// catches a URL made of x alone. The remote name the message quotes is
// not input, so a line that reads like it is no finding (#390).
func urlRefusalProblem(parse urlParser, out []byte, lim limits) string {
	_, ref := parse(out, "origin", lim)
	if ref == nil {
		return ""
	}
	if ref.Rule == "" {
		return fmt.Sprintf("parseURLs(%q): refusal without a rule", out)
	}
	for _, c := range []byte{'x', 'y'} {
		masked := maskLines(out, c)
		if _, maskedRef := parse(masked, "origin", lim); maskedRef == nil || maskedRef.Error() != ref.Error() {
			return fmt.Sprintf("parseURLs(%q): refusal %q quotes a URL: the input masked to %q gives %v", out, ref, masked, maskedRef)
		}
	}
	return ""
}

// maskLines replaces every byte of out but the line ends with c.
func maskLines(out []byte, c byte) []byte {
	m := bytes.Clone(out)
	for i := range m {
		if m[i] != '\n' {
			m[i] = c
		}
	}
	return m
}

// TestURLRefusalProblem: the oracle passes parseURLs on the input that
// a quoted remote name made it fail on (#390), and fails a parser whose
// refusal quotes a URL or names no rule.
func TestURLRefusalProblem(t *testing.T) {
	lim := limits{remotes: 4, remoteName: 16, urls: 4, urlLen: 64, roots: 4}
	quotesURL := func(out []byte, name string, lim limits) ([]string, *Refusal) {
		urls, ref := parseURLs(out, name, lim)
		if ref != nil && ref.Rule == RuleURLTooLong {
			ref = &Refusal{Rule: ref.Rule, msg: "URL " + quoted(string(bytes.TrimSuffix(out, []byte("\n")))) + " is too long"}
		}
		return urls, ref
	}
	noRule := func(out []byte, name string, lim limits) ([]string, *Refusal) {
		urls, ref := parseURLs(out, name, lim)
		if ref != nil {
			ref = &Refusal{msg: ref.msg}
		}
		return urls, ref
	}
	long := "https://u:" + strings.Repeat("t", 64) + "@h.example/x\n"
	for _, tc := range []struct {
		name  string
		parse urlParser
		in    string
		fails bool
	}{
		{"quoted remote name", parseURLs, "origin\"", false},
		{"no line end", parseURLs, "https://u:tokentoken@h.example/x", false},
		{"too long", parseURLs, long, false},
		{"accepted", parseURLs, "https://h.example/x\n", false},
		{"quotes a URL", quotesURL, long, true},
		{"quotes a URL of x", quotesURL, strings.Repeat("x", 65) + "\n", true},
		{"no rule", noRule, "origin\"", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problem := urlRefusalProblem(tc.parse, []byte(tc.in), lim)
			if (problem != "") != tc.fails {
				t.Errorf("urlRefusalProblem(%q) = %q, want a finding: %v", tc.in, problem, tc.fails)
			}
		})
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
