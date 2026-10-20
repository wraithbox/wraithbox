package project

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestResolveWorktrees: a main worktree, a linked worktree of it and a
// subdirectory of each resolve to one key. A submodule resolves to a key
// of its own (C04-projects, "Remotes and root commits", the second
// `git worktree`; S05-04-naming, "Project key").
func TestResolveWorktrees(t *testing.T) {
	tmp := tempDir(t)
	main := newRepo(t, filepath.Join(tmp, "foo"))
	mkdir(t, filepath.Join(main, "a", "b"))
	wt := filepath.Join(tmp, "foo-wt")
	gitT(t, main, "worktree", "add", "-q", wt)
	mkdir(t, filepath.Join(wt, "c"))
	// A back-link relative to the worktree's git directory, where git
	// knows worktree.useRelativePaths.
	rel := filepath.Join(tmp, "foo-rel")
	gitT(t, main, "-c", "worktree.useRelativePaths=true", "worktree", "add", "-q", rel)
	if link, err := os.ReadFile(filepath.Join(main, ".git", "worktrees", "foo-rel", "gitdir")); err == nil {
		t.Logf("back-link of foo-rel: %q", link)
	}
	sub := newRepo(t, filepath.Join(tmp, "lib"))
	gitT(t, main, "submodule", "add", "-q", sub, "mods/sub")

	r, _ := testResolver(t, emptyGlobal(t))
	want := Key{Realm: RealmNative, Path: filepath.Join(main, ".git")}
	for _, c := range []struct {
		dir  string
		kind Kind
	}{
		{main, KindMain},
		{filepath.Join(main, "a", "b"), KindMain},
		{wt, KindWorktree},
		{filepath.Join(wt, "c"), KindWorktree},
		{rel, KindWorktree},
	} {
		repo := resolveOK(t, r, c.dir)
		if repo.Key != want || repo.Kind != c.kind || repo.Name != "foo" {
			t.Errorf("Resolve(%s) = %+v, want key %+v, kind %s, name foo", c.dir, repo, want, c.kind)
		}
	}

	repo := resolveOK(t, r, filepath.Join(main, "mods", "sub"))
	wantSub := Key{Realm: RealmNative, Path: filepath.Join(main, ".git", "modules", "mods", "sub")}
	if repo.Key != wantSub || repo.Kind != KindSubmodule || repo.Name != "sub" {
		t.Errorf("Resolve(submodule) = %+v, want key %+v, kind %s, name sub", repo, wantSub, KindSubmodule)
	}
}

// TestResolveRelativeDir: a relative directory is taken from the
// current directory.
func TestResolveRelativeDir(t *testing.T) {
	main := newRepo(t, filepath.Join(tempDir(t), "foo"))
	t.Chdir(mkdir(t, filepath.Join(main, "x")))
	r, _ := testResolver(t, emptyGlobal(t))
	if repo := resolveOK(t, r, "."); repo.Dir != main {
		t.Errorf("Resolve(.) = %+v, want dir %s", repo, main)
	}
}

// TestResolveRefusesGitElsewhere: a `.git` file or symbolic link that
// names project A's repository, one of its submodules or one of its
// linked worktrees is refused with the reason, before any identity is
// read (C04-projects, "Git directories that point elsewhere";
// SEC08-proj-isolation).
func TestResolveRefusesGitElsewhere(t *testing.T) {
	tmp := tempDir(t)
	a := newRepo(t, filepath.Join(tmp, "a"))
	sub := newRepo(t, filepath.Join(tmp, "lib"))
	gitT(t, a, "submodule", "add", "-q", sub, "sub")
	gitT(t, a, "worktree", "add", "-q", filepath.Join(tmp, "a-wt"))
	aGit := filepath.Join(a, ".git")

	tests := []struct {
		name string
		make func(dir string)
		rule Rule
	}{
		{"file names A", func(dir string) { writeFile(t, filepath.Join(dir, ".git"), "gitdir: "+aGit+"\n") }, RuleGitFileElsewhere},
		{"file names A relatively", func(dir string) { writeFile(t, filepath.Join(dir, ".git"), "gitdir: ../a/.git\n") }, RuleGitFileElsewhere},
		{"file names A's submodule", func(dir string) {
			writeFile(t, filepath.Join(dir, ".git"), "gitdir: "+filepath.Join(aGit, "modules", "sub")+"\n")
		}, RuleGitFileElsewhere},
		{"file names A's worktree", func(dir string) {
			writeFile(t, filepath.Join(dir, ".git"), "gitdir: "+filepath.Join(aGit, "worktrees", "a-wt")+"\n")
		}, RuleGitFileElsewhere},
		{"link to A", func(dir string) { symlink(t, aGit, filepath.Join(dir, ".git")) }, RuleGitLink},
		{"link to A's worktree file", func(dir string) { symlink(t, filepath.Join(tmp, "a-wt", ".git"), filepath.Join(dir, ".git")) }, RuleGitLink},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := mkdir(t, filepath.Join(tmp, "x"+string(rune('0'+i))))
			tt.make(dir)
			r, log := testResolver(t, emptyGlobal(t))
			if tt.rule == RuleGitLink {
				// A git that doesn't exist: the link is refused before
				// git runs and follows it.
				r.Git = filepath.Join(tmp, "no-git")
			}
			repo, err := r.Resolve(t.Context(), mkdir(t, filepath.Join(dir, "deeper")))
			if repo != nil {
				t.Fatalf("Resolve = %+v, want a refusal", repo)
			}
			requireRule(t, err, tt.rule, log)
			t.Logf("refused: %v", err)
			if strings.Contains(log.String(), "identity") {
				t.Errorf("log = %s: an identity was read", log)
			}
		})
	}
}

// TestResolveIgnoresGitEnvironment: GIT_* variables in wb's environment
// that point at or rewrite project A don't change what a directory
// resolves to, or its identity (C04-projects, "Git directories that
// point elsewhere"; SEC03-no-host-exec).
func TestResolveIgnoresGitEnvironment(t *testing.T) {
	tmp := tempDir(t)
	a := newRepo(t, filepath.Join(tmp, "a"))
	gitT(t, a, "remote", "add", "origin", "https://a.example/a.git")
	b := newRepo(t, filepath.Join(tmp, "b"))
	aGit := filepath.Join(a, ".git")
	for k, v := range map[string]string{
		"GIT_DIR":                 aGit,
		"GIT_COMMON_DIR":          aGit,
		"GIT_WORK_TREE":           a,
		"GIT_CONFIG_PARAMETERS":   "'remote.origin.url'='https://a.example/a.git'",
		"GIT_CONFIG_COUNT":        "1",
		"GIT_CONFIG_KEY_0":        "remote.origin.url",
		"GIT_CONFIG_VALUE_0":      "https://a.example/a.git",
		"GIT_CEILING_DIRECTORIES": tmp,
	} {
		t.Setenv(k, v)
	}
	r, _ := testResolver(t, emptyGlobal(t))
	repo := resolveOK(t, r, b)
	if want := filepath.Join(b, ".git"); repo.Key.Path != want {
		t.Errorf("key = %s, want %s", repo.Key.Path, want)
	}
	id, err := r.Identity(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(id.URLs) != 0 || len(id.RootCommits) != 1 {
		t.Errorf("identity = %+v, want b's one root commit and no URL", id)
	}
}

// TestResolveCaseAndLinks: one repository reached as <tmp>/git/foo,
// <tmp>/Git/foo and through a symbolic link gives equal keys
// (C04-projects, "Moves and second clones", the macOS case;
// S05-04-naming, "Path").
func TestResolveCaseAndLinks(t *testing.T) {
	tmp := tempDir(t)
	foo := newRepo(t, filepath.Join(tmp, "git", "foo"))
	if _, err := os.Stat(filepath.Join(tmp, "Git", "foo")); err != nil {
		t.Skipf("the volume is case-sensitive: %v", err)
	}
	link := symlink(t, foo, filepath.Join(tmp, "link"))
	r, _ := testResolver(t, emptyGlobal(t))
	want := resolveOK(t, r, foo).Key
	for _, dir := range []string{filepath.Join(tmp, "Git", "foo"), filepath.Join(tmp, "GIT", "FOO"), link} {
		if got := resolveOK(t, r, dir); got.Key != want || got.Dir != foo {
			t.Errorf("Resolve(%s) = %+v, want key %+v and dir %s", dir, got, want, foo)
		}
	}
}

// TestResolveRefusesOwnDirectories: a repository under <data>, <config>
// or <logs> is refused, also when the directory is spelled in another
// letter case or reached through a link (S05-04-naming, "Wraith Box's
// own directories").
func TestResolveRefusesOwnDirectories(t *testing.T) {
	for _, field := range []string{"data", "config", "logs"} {
		t.Run(field, func(t *testing.T) {
			r, _ := testResolver(t, emptyGlobal(t))
			root := mkdir(t, r.ownField(field))
			repo := newRepo(t, filepath.Join(root, "projects", "p", "landing"))
			tmp := filepath.Dir(filepath.Dir(root))
			for name, own := range map[string]string{
				"as is":       root,
				"other case":  filepath.Join(filepath.Dir(root), strings.ToUpper(field)),
				"linked root": symlink(t, root, filepath.Join(tmp, "link-"+field)),
			} {
				r, log := testResolver(t, emptyGlobal(t))
				r.setOwnField(field, own)
				for _, dir := range []string{repo, symlink(t, repo, filepath.Join(tmp, "repo-"+field+"-"+strings.ReplaceAll(name, " ", "-")))} {
					log.Reset()
					_, err := r.Resolve(t.Context(), dir)
					requireRule(t, err, RuleOwnDirectory, log)
				}
			}
		})
	}
}

func (r *Resolver) ownField(field string) string {
	switch field {
	case "data":
		return r.Own.Data
	case "config":
		return r.Own.Config
	}
	return r.Own.Logs
}

func (r *Resolver) setOwnField(field, dir string) {
	switch field {
	case "data":
		r.Own.Data = dir
	case "config":
		r.Own.Config = dir
	default:
		r.Own.Logs = dir
	}
}

// TestResolveFailingRevParse: a repository with a corrupt HEAD is
// refused with git's error, standing alone, and when git walks on to a
// parent repository (S05-04-naming, "Failure").
func TestResolveFailingRevParse(t *testing.T) {
	tmp := tempDir(t)
	alone := newRepo(t, filepath.Join(tmp, "alone"))
	writeFile(t, filepath.Join(alone, ".git", "HEAD"), "garbage\n")
	r, log := testResolver(t, emptyGlobal(t))
	_, err := r.Resolve(t.Context(), alone)
	requireRule(t, err, RuleGitFailed, log)
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("err = %v, want git's error line", err)
	}

	parent := newRepo(t, filepath.Join(tmp, "parent"))
	nested := newRepo(t, filepath.Join(parent, "nested"))
	writeFile(t, filepath.Join(nested, ".git", "HEAD"), "garbage\n")
	log.Reset()
	_, err = r.Resolve(t.Context(), nested)
	requireRule(t, err, RuleGitDirElsewhere, log)
}

// TestResolveRefusesOtherEntryType: a `.git` that is neither a
// directory, a file nor a link, here a FIFO, is refused before git runs.
func TestResolveRefusesOtherEntryType(t *testing.T) {
	dir := tempDir(t)
	if err := syscall.Mkfifo(filepath.Join(dir, ".git"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, log := testResolver(t, emptyGlobal(t))
	r.Git = filepath.Join(dir, "no-git")
	_, err := r.Resolve(t.Context(), dir)
	requireRule(t, err, RuleGitEntryType, log)
}

// TestResolveNotInRepo: no `.git` up to the root is refused.
func TestResolveNotInRepo(t *testing.T) {
	r, log := testResolver(t, emptyGlobal(t))
	_, err := r.Resolve(t.Context(), tempDir(t))
	requireRule(t, err, RuleNotInRepo, log)
}

// TestResolveMissingDir: a directory that doesn't exist is refused.
func TestResolveMissingDir(t *testing.T) {
	r, log := testResolver(t, emptyGlobal(t))
	_, err := r.Resolve(t.Context(), filepath.Join(tempDir(t), "missing"))
	requireRule(t, err, RuleDirUnreadable, log)
}
