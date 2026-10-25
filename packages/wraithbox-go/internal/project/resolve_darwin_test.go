package project

import (
	"os"
	"path/filepath"
	"strconv"
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

// TestResolveWorktreeOtherCase: a linked worktree whose `.git` file
// names the common directory in another letter case, or through a
// link, gives the key of its main checkout: the key is the canonical
// form, not the path git prints (S05-04-naming, "Path").
func TestResolveWorktreeOtherCase(t *testing.T) {
	tmp := tempDir(t)
	foo := newRepo(t, filepath.Join(tmp, "git", "foo"))
	if _, err := os.Stat(filepath.Join(tmp, "GIT", "FOO")); err != nil {
		t.Skipf("the volume is case-sensitive: %v", err)
	}
	link := symlink(t, filepath.Join(tmp, "git"), filepath.Join(tmp, "link"))
	r, _ := testResolver(t, emptyGlobal(t))
	want := resolveOK(t, r, foo).Key
	for i, base := range []string{filepath.Join(tmp, "GIT", "FOO"), filepath.Join(link, "foo")} {
		wt := filepath.Join(tmp, "wt"+strconv.Itoa(i))
		gitT(t, foo, "worktree", "add", "-q", wt)
		name := filepath.Base(wt)
		writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(base, ".git", "worktrees", name)+"\n")
		repo := resolveOK(t, r, wt)
		if repo.Key != want || repo.Kind != KindWorktree || repo.Name != "foo" {
			t.Errorf("Resolve(%s) through %s = %+v, want key %+v, a worktree named foo", wt, base, repo, want)
		}
	}
}

// TestCheckOwnRefusesControlInCanonicalKey: a common directory reached
// through a clean link is refused when the link's target name holds a
// control or format character, because the key is the canonical path
// (S05-04-naming, "Path"). git resolves links in the common directory
// it prints, and that output is checked first, so Resolve can't reach
// this check with git's own output. The test calls it directly.
func TestCheckOwnRefusesControlInCanonicalKey(t *testing.T) {
	for name, base := range map[string]string{"escape": "a\x1b[2Jb", "right-to-left override": "a\u202eb"} {
		t.Run(name, func(t *testing.T) {
			tmp := tempDir(t)
			newRepo(t, filepath.Join(tmp, base, "foo"))
			link := symlink(t, filepath.Join(tmp, base), filepath.Join(tmp, "link"))
			r, _ := testResolver(t, emptyGlobal(t))
			_, ref := r.checkOwn(filepath.Join(link, "foo", ".git"))
			if ref == nil || ref.Rule != RuleDirUnreadable {
				t.Fatalf("checkOwn through %s = %v, want the rule %s", link, ref, RuleDirUnreadable)
			}
		})
	}
}

// TestResolveRefusesOwnDirectories: a repository under <data>, <config>
// or <logs> is refused, also when the directory is spelled in another
// letter case or reached through a link (C04-projects, "Git directories
// that point elsewhere"; S05-04-naming, "Wraith Box's own directories").
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
// parent repository (C04-projects, "Git directories that point
// elsewhere"; S05-04-naming, "Failure").
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

// TestResolveRefusesControlInDir: a directory whose name holds a
// control character or isn't UTF-8 is refused, as a path git prints
// would be, before git runs.
func TestResolveRefusesControlInDir(t *testing.T) {
	for name, base := range map[string]string{"escape": "a\x1b[2Jb", "line end": "a\nb"} {
		t.Run(name, func(t *testing.T) {
			dir := newRepo(t, filepath.Join(tempDir(t), base))
			r, log := testResolver(t, emptyGlobal(t))
			r.Git = filepath.Join(dir, "no-git")
			_, err := r.Resolve(t.Context(), dir)
			requireRule(t, err, RuleDirUnreadable, log)
		})
	}
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

// TestResolveCoreWorktree: a `.git` directory whose core.worktree, in
// config or config.worktree, names another project's working tree, or
// anything but the directory that holds it, is refused before any
// identity is read. One that names that directory, also relatively or
// through a link, is accepted (C04-projects, "Git directories that
// point elsewhere"; S05-04-naming, "Which directory";
// SEC08-proj-isolation).
func TestResolveCoreWorktree(t *testing.T) {
	tmp := tempDir(t)
	a := newRepo(t, filepath.Join(tmp, "a"))
	link := symlink(t, tmp, filepath.Join(tmp, "link"))
	file := filepath.Join(tmp, "file")
	writeFile(t, file, "")

	tests := []struct {
		name  string
		file  string
		value func(x string) string
		rule  Rule
	}{
		{"names A", "config", func(string) string { return a }, RuleWorktreeElsewhere},
		{"names A relatively", "config", func(string) string { return "../../a" }, RuleWorktreeElsewhere},
		{"names A in config.worktree", "config.worktree", func(string) string { return a }, RuleWorktreeElsewhere},
		{"names A's subdirectory", "config", func(string) string { return mkdir(t, filepath.Join(a, "sub")) }, RuleWorktreeElsewhere},
		{"names the parent", "config", func(string) string { return ".." + string(filepath.Separator) + ".." }, RuleWorktreeElsewhere},
		{"names a file", "config", func(string) string { return file }, RuleWorktreeElsewhere},
		{"names nothing", "config", func(string) string { return filepath.Join(tmp, "missing") }, RuleWorktreeElsewhere},
		// git itself refuses an empty value.
		{"empty", "config", func(string) string { return "" }, RuleGitFailed},
		{"names itself", "config", func(x string) string { return x }, ""},
		{"names itself relatively", "config", func(string) string { return ".." }, ""},
		{"names itself through a link", "config", func(x string) string { return filepath.Join(link, filepath.Base(x)) }, ""},
		{"names itself in config.worktree", "config.worktree", func(x string) string { return x }, ""},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			x := newRepo(t, filepath.Join(tmp, "x"+strconv.Itoa(i)))
			gitT(t, x, "config", "--file", filepath.Join(x, ".git", tt.file), "core.worktree", tt.value(x))
			r, log := testResolver(t, emptyGlobal(t))
			repo, err := r.Resolve(t.Context(), x)
			if tt.rule == "" {
				if err != nil || repo.Dir != x || repo.Kind != KindMain {
					t.Fatalf("Resolve = %+v, %v; want dir %s, kind main", repo, err, x)
				}
				return
			}
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

// TestResolveCoreWorktreeMalformed: a config.worktree that git can't
// parse is refused, not taken as unset, even with
// extensions.worktreeConfig off, when git itself ignores the file.
func TestResolveCoreWorktreeMalformed(t *testing.T) {
	x := newRepo(t, filepath.Join(tempDir(t), "x"))
	writeFile(t, filepath.Join(x, ".git", "config.worktree"), "[core\nworktree = /\n")
	r, log := testResolver(t, emptyGlobal(t))
	repo, err := r.Resolve(t.Context(), x)
	if repo != nil {
		t.Fatalf("Resolve = %+v, want a refusal", repo)
	}
	t.Logf("refused: %v", err)
	requireRule(t, err, RuleGitFailed, log)
}

// TestResolveCoreWorktreeUnreadable: a config.worktree that exists but
// isn't a regular file that can be opened is refused before git reads
// it, because git takes a file it can't read as one without the value.
func TestResolveCoreWorktreeUnreadable(t *testing.T) {
	tmp := tempDir(t)
	for name, build := range map[string]func(p string){
		"directory": func(p string) { mkdir(t, p) },
		"mode 000": func(p string) {
			writeFile(t, p, "[core]\n\tworktree = /\n")
			if err := os.Chmod(p, 0); err != nil {
				t.Fatal(err)
			}
		},
		"link": func(p string) { symlink(t, filepath.Join(tmp, "elsewhere"), p) },
	} {
		t.Run(name, func(t *testing.T) {
			x := newRepo(t, filepath.Join(tmp, strings.ReplaceAll(name, " ", "-")))
			build(filepath.Join(x, ".git", "config.worktree"))
			r, log := testResolver(t, emptyGlobal(t))
			repo, err := r.Resolve(t.Context(), x)
			if repo != nil {
				t.Fatalf("Resolve = %+v, want a refusal", repo)
			}
			t.Logf("refused: %v", err)
			requireRule(t, err, RuleDirUnreadable, log)
		})
	}
}

// TestResolveLinkedWorktreeCoreWorktree: a linked worktree, as an
// archive could hold it next to its repository, whose core.worktree in
// the common config or in the worktree's config.worktree names another
// project's tree is refused before any identity is read. One that names
// the worktree itself is accepted (C04-projects, "Git directories that
// point elsewhere"; S05-04-naming, "A `.git` file";
// SEC08-proj-isolation).
func TestResolveLinkedWorktreeCoreWorktree(t *testing.T) {
	tmp := tempDir(t)
	a := newRepo(t, filepath.Join(tmp, "a"))
	tests := []struct {
		name  string
		file  func(repo, wt string) string
		value func(wt string) string
		rule  Rule
	}{
		{"common config names A", func(repo, _ string) string { return filepath.Join(repo, ".git", "config") }, func(string) string { return a }, RuleWorktreeElsewhere},
		{"config.worktree names A", func(repo, wt string) string {
			return filepath.Join(repo, ".git", "worktrees", filepath.Base(wt), "config.worktree")
		}, func(string) string { return a }, RuleWorktreeElsewhere},
		{"config.worktree names itself", func(repo, wt string) string {
			return filepath.Join(repo, ".git", "worktrees", filepath.Base(wt), "config.worktree")
		}, func(wt string) string { return wt }, ""},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newRepo(t, filepath.Join(tmp, "repo"+strconv.Itoa(i)))
			wt := filepath.Join(tmp, "wt"+strconv.Itoa(i))
			gitT(t, repo, "worktree", "add", "-q", wt)
			gitT(t, repo, "config", "--file", tt.file(repo, wt), "core.worktree", tt.value(wt))
			r, log := testResolver(t, emptyGlobal(t))
			got, err := r.Resolve(t.Context(), wt)
			if tt.rule == "" {
				if err != nil || got.Dir != wt || got.Kind != KindWorktree {
					t.Fatalf("Resolve = %+v, %v; want dir %s, kind worktree", got, err, wt)
				}
				return
			}
			if got != nil {
				t.Fatalf("Resolve = %+v, want a refusal", got)
			}
			requireRule(t, err, tt.rule, log)
			t.Logf("refused: %v", err)
			if strings.Contains(log.String(), "identity") {
				t.Errorf("log = %s: an identity was read", log)
			}
		})
	}
}

// TestResolveSubmoduleWorktreeConfig: a submodule whose config.worktree
// names another directory is refused, even though its config names the
// submodule's own (S05-04-naming, "A `.git` file").
func TestResolveSubmoduleWorktreeConfig(t *testing.T) {
	tmp := tempDir(t)
	main := newRepo(t, filepath.Join(tmp, "foo"))
	other := newRepo(t, filepath.Join(tmp, "other"))
	sub := newRepo(t, filepath.Join(tmp, "lib"))
	gitT(t, main, "submodule", "add", "-q", sub, "sub")
	gitDir := filepath.Join(main, ".git", "modules", "sub")
	gitT(t, main, "config", "--file", filepath.Join(gitDir, "config.worktree"), "core.worktree", other)
	r, log := testResolver(t, emptyGlobal(t))
	_, err := r.Resolve(t.Context(), filepath.Join(main, "sub"))
	requireRule(t, err, RuleGitFileElsewhere, log)
}
