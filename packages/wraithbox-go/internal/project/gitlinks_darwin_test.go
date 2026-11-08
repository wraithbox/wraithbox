package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveRefusesGitDirLinksOutside: a `.git` directory that holds a
// symbolic link into project A's `.git`, at any of the entries git
// reads, is refused with its rule before git runs, and nothing about
// the repository is read (C04-projects, "Git directories that point
// elsewhere"; SEC08-proj-isolation).
func TestResolveRefusesGitDirLinksOutside(t *testing.T) {
	tmp := tempDir(t)
	aGit := filepath.Join(newRepo(t, filepath.Join(tmp, "a")), ".git")
	gitT(t, filepath.Dir(aGit), "worktree", "add", "-q", filepath.Join(tmp, "a-wt"))

	tests := []struct {
		name  string
		entry string
		// target is where the link points. A relative one is used as
		// written.
		target string
		rule   Rule
	}{
		{"config", "config", filepath.Join(aGit, "config"), RuleGitDirLinkOutside},
		{"config.worktree", "config.worktree", filepath.Join(aGit, "config"), RuleGitDirLinkOutside},
		{"commondir", "commondir", aGit, RuleGitDirLinkOutside},
		{"HEAD", "HEAD", filepath.Join(aGit, "HEAD"), RuleGitDirLinkOutside},
		{"objects", "objects", filepath.Join(aGit, "objects"), RuleGitDirLinkOutside},
		{"refs", "refs", filepath.Join(aGit, "refs"), RuleGitDirLinkOutside},
		{"worktrees", "worktrees", filepath.Join(aGit, "worktrees"), RuleGitDirLinkOutside},
		{"packed-refs", "packed-refs", filepath.Join(aGit, "HEAD"), RuleGitDirLinkOutside},
		{"nested ref", filepath.Join("refs", "heads", "other"), filepath.Join(aGit, "refs", "heads", "main"), RuleGitDirLinkOutside},
		{"nested pack directory", filepath.Join("objects", "pack-a"), filepath.Join(aGit, "objects", "pack"), RuleGitDirLinkOutside},
		{"relative link out", "packed-refs", filepath.Join("..", "..", "a", ".git", "HEAD"), RuleGitDirLinkOutside},
		{"link to the working tree", "description", filepath.Join("..", "README"), RuleGitDirLinkOutside},
		{"dangling", "info/grafts", filepath.Join(tmp, "absent"), RuleGitDirLinkUnresolved},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newRepo(t, filepath.Join(tmp, "x"+string(rune('a'+i))))
			writeFile(t, filepath.Join(dir, "README"), "")
			link := filepath.Join(dir, ".git", tt.entry)
			if err := os.RemoveAll(link); err != nil {
				t.Fatal(err)
			}
			mkdir(t, filepath.Dir(link))
			symlink(t, tt.target, link)
			r, log := testResolver(t, emptyGlobal(t))
			// A git that doesn't exist: the link is refused before git
			// runs and follows it.
			r.Git = filepath.Join(tmp, "no-git")
			repo, err := r.Resolve(t.Context(), dir)
			if repo != nil {
				t.Fatalf("Resolve = %+v, want a refusal", repo)
			}
			requireRule(t, err, tt.rule, log)
			if !strings.Contains(err.Error(), tt.entry) {
				t.Errorf("err = %v, want the entry %s named", err, tt.entry)
			}
			t.Logf("refused: %v", err)
		})
	}
}

// TestResolveAcceptsGitDirLinksInside: a link that stays in the `.git`
// directory, and a hook linked to the working tree, which git never
// reads with core.hooksPath set to /dev/null, are accepted
// (C04-projects, "Git directories that point elsewhere").
func TestResolveAcceptsGitDirLinksInside(t *testing.T) {
	tmp := tempDir(t)
	tests := []struct {
		name   string
		entry  string
		target string
	}{
		{"relative ref", filepath.Join("refs", "heads", "alias"), "main"},
		{"absolute objects directory", filepath.Join("objects", "alias"), "<git>/objects/pack"},
		{"link to the git directory", "self", "."},
		{"hook to the working tree", filepath.Join("hooks", "pre-commit"), filepath.Join("..", "..", "README")},
		{"hooks directory to the working tree", "hooks", ".."},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newRepo(t, filepath.Join(tmp, "x"+string(rune('a'+i))))
			gitDir := filepath.Join(dir, ".git")
			link := filepath.Join(gitDir, tt.entry)
			if err := os.RemoveAll(link); err != nil {
				t.Fatal(err)
			}
			symlink(t, strings.Replace(tt.target, "<git>", gitDir, 1), link)
			r, _ := testResolver(t, emptyGlobal(t))
			repo := resolveOK(t, r, dir)
			if repo.Key.Path != gitDir {
				t.Errorf("key = %s, want %s", repo.Key.Path, gitDir)
			}
		})
	}
}

// TestResolveRefusesLinksInWorktreeAndSubmodule: the git directory a
// `.git` file names is checked too. A linked worktree's `config.worktree`
// linked out of the common directory, and a submodule's `objects`
// linked into its parent repository's, are refused before git reads
// their core.worktree (C04-projects, "Git directories that point
// elsewhere").
func TestResolveRefusesLinksInWorktreeAndSubmodule(t *testing.T) {
	tmp := tempDir(t)
	other := filepath.Join(newRepo(t, filepath.Join(tmp, "other")), ".git")
	a := newRepo(t, filepath.Join(tmp, "a"))
	sub := newRepo(t, filepath.Join(tmp, "lib"))
	gitT(t, a, "submodule", "add", "-q", sub, "sub")
	wt := filepath.Join(tmp, "a-wt")
	gitT(t, a, "worktree", "add", "-q", wt)
	aGit := filepath.Join(a, ".git")

	symlink(t, filepath.Join(other, "config"), filepath.Join(aGit, "worktrees", "a-wt", "config.worktree"))
	subObjects := filepath.Join(aGit, "modules", "sub", "objects")
	if err := os.RemoveAll(subObjects); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(aGit, "objects"), subObjects)

	for _, dir := range []string{wt, filepath.Join(a, "sub")} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			r, log := testResolver(t, emptyGlobal(t))
			repo, err := r.Resolve(t.Context(), dir)
			if repo != nil {
				t.Fatalf("Resolve = %+v, want a refusal", repo)
			}
			// Had git read core.worktree first, the config.worktree link
			// would be refused as a config file that isn't a regular
			// file, with another rule.
			requireRule(t, err, RuleGitDirLinkOutside, log)
		})
	}
}
