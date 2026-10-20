package project

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// appendConfig adds text to a repository's own configuration file, for
// remotes that `git remote add` would refuse.
func appendConfig(t *testing.T, repo, text string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(repo, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func identityOf(t *testing.T, r *Resolver, dir string) (Identity, error) {
	t.Helper()
	return r.Identity(t.Context(), resolveOK(t, r, dir))
}

// TestIdentityURLs: the SSH and HTTPS forms of one remote give one
// compare form, insteadOf from the global configuration is applied, and
// a token in the user info appears neither in the identity nor in the
// log (C04-projects, "Remotes and root commits"; S05-04-naming,
// "Recorded identity").
func TestIdentityURLs(t *testing.T) {
	tmp := tempDir(t)
	global := filepath.Join(tmp, "gitconfig")
	writeFile(t, global, "[url \"https://forge.example/\"]\n\tinsteadOf = forge:\n")
	repo := newRepo(t, filepath.Join(tmp, "repo"))
	gitT(t, repo, "remote", "add", "origin", "git@github.com:org/repo.git")
	gitT(t, repo, "remote", "add", "https", "https://x-access-token:ghp_secret0123@github.com/org/repo.git")
	gitT(t, repo, "remote", "add", "short", "forge:team/tool.git")
	gitT(t, repo, "remote", "set-url", "--add", "short", "http://u:pw_secret@plain.example/x?q=1")

	r, log := testResolver(t, global)
	id, err := identityOf(t, r, repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"http://plain.example/x",
		"https://forge.example/team/tool.git",
		"https://github.com/org/repo.git",
	}
	if !slices.Equal(id.URLs, want) || id.RootCommits != nil {
		t.Errorf("identity = %+v, want URLs %q and no root commits", id, want)
	}
	for _, secret := range []string{"ghp_secret0123", "pw_secret"} {
		if strings.Contains(log.String(), secret) || slices.ContainsFunc(id.URLs, func(u string) bool { return strings.Contains(u, secret) }) {
			t.Errorf("identity %q or log %s holds %s", id.URLs, log, secret)
		}
	}
	if !strings.Contains(log.String(), `"rule":"project-identity-read"`) {
		t.Errorf("log = %s, want the identity decision", log)
	}
}

// TestIdentityRootCommits: a repository without a remote gives its root
// commits, and one without commits gives an empty identity
// (C04-projects, "Remotes and root commits").
func TestIdentityRootCommits(t *testing.T) {
	tmp := tempDir(t)
	repo := newRepo(t, filepath.Join(tmp, "repo"))
	first := gitT(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "checkout", "-q", "--orphan", "other")
	gitT(t, repo, "commit", "-q", "--allow-empty", "-m", "second root")
	second := gitT(t, repo, "rev-parse", "HEAD")

	r, _ := testResolver(t, emptyGlobal(t))
	id, err := identityOf(t, r, repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{first, second}
	slices.Sort(want)
	if id.URLs != nil || !slices.Equal(id.RootCommits, want) {
		t.Errorf("identity = %+v, want root commits %q", id, want)
	}

	empty := mkdir(t, filepath.Join(tmp, "empty"))
	gitT(t, empty, "init", "-q")
	id, err = identityOf(t, r, empty)
	if err != nil || id.URLs != nil || id.RootCommits != nil {
		t.Errorf("identity of a repository without commits = %+v, %v; want empty", id, err)
	}
}

// TestIdentityRefusesHostileRemoteName: a remote name that git would
// read as an option, or with a control character, is refused with its
// rule (S05-04-naming, "Recorded identity").
func TestIdentityRefusesHostileRemoteName(t *testing.T) {
	for name, section := range map[string]string{
		"option":  "[remote \"--upload-pack=touch pwned\"]\n\turl = https://h.example/x\n",
		"dash":    "[remote \"-x\"]\n\turl = https://h.example/x\n",
		"control": "[remote \"a\tb\"]\n\turl = https://h.example/x\n",
	} {
		t.Run(name, func(t *testing.T) {
			repo := newRepo(t, filepath.Join(tempDir(t), "repo"))
			appendConfig(t, repo, section)
			r, log := testResolver(t, emptyGlobal(t))
			_, err := identityOf(t, r, repo)
			requireRule(t, err, RuleRemoteName, log)
		})
	}
}

// TestIdentityCaps: each cap refuses the identity with its own rule,
// never cuts it (SEC13-bounded-resources).
func TestIdentityCaps(t *testing.T) {
	small := limits{remotes: 2, remoteName: 8, urls: 2, urlLen: 40, roots: 1}
	tests := []struct {
		name  string
		setup func(t *testing.T, repo string)
		rule  Rule
	}{
		{"remotes", func(t *testing.T, repo string) {
			for _, n := range []string{"a", "b", "c"} {
				gitT(t, repo, "remote", "add", n, "https://h.example/"+n)
			}
		}, RuleTooManyRemotes},
		{"remote name", func(t *testing.T, repo string) {
			gitT(t, repo, "remote", "add", "remote-12", "https://h.example/x")
		}, RuleRemoteName},
		{"urls", func(t *testing.T, repo string) {
			gitT(t, repo, "remote", "add", "o", "https://h.example/1")
			gitT(t, repo, "remote", "set-url", "--add", "o", "https://h.example/2")
			gitT(t, repo, "remote", "set-url", "--add", "o", "https://h.example/3")
		}, RuleTooManyURLs},
		{"url length", func(t *testing.T, repo string) {
			gitT(t, repo, "remote", "add", "o", "https://h.example/"+strings.Repeat("x", 30))
		}, RuleURLTooLong},
		{"root commits", func(t *testing.T, repo string) {
			gitT(t, repo, "checkout", "-q", "--orphan", "other")
			gitT(t, repo, "commit", "-q", "--allow-empty", "-m", "second root")
		}, RuleTooManyRoots},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newRepo(t, filepath.Join(tempDir(t), "repo"))
			tt.setup(t, repo)
			r, log := testResolver(t, emptyGlobal(t))
			r.limits = &small
			id, err := identityOf(t, r, repo)
			if id.URLs != nil || id.RootCommits != nil {
				t.Errorf("identity = %+v, want none", id)
			}
			requireRule(t, err, tt.rule, log)
		})
	}
}

// TestIdentityNeedsResolvedRepo: Identity reads nothing without a
// repository from Resolve.
func TestIdentityNeedsResolvedRepo(t *testing.T) {
	r, _ := testResolver(t, emptyGlobal(t))
	if _, err := r.Identity(t.Context(), &Repo{}); err == nil {
		t.Error("Identity of an empty Repo: want an error")
	}
}
