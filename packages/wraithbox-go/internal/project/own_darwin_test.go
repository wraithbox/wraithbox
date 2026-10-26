package project

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveOwnDirectoryBeforeGit: a directory in <data>, <config> or
// <logs> is refused with RuleOwnDirectory before git runs, also when a
// repository above them would hold it, so git never runs in a directory
// wb-hostd can write (S04-06-git-confinement).
func TestResolveOwnDirectoryBeforeGit(t *testing.T) {
	for _, field := range []string{"data", "config", "logs"} {
		t.Run(field, func(t *testing.T) {
			r, log := testResolver(t, emptyGlobal(t))
			own := r.ownField(field)
			// A repository above the own directories, which the walk up
			// from a directory inside them would reach.
			newRepo(t, filepath.Dir(filepath.Dir(own)))
			bare := mkdir(t, filepath.Join(own, "projects", "p", "landing.git"))
			gitT(t, bare, "init", "-q", "--bare")
			marker := filepath.Join(tempDir(t), "git-ran")
			r.Git = filepath.Join(tempDir(t), "git")
			writeFile(t, r.Git, "#!/bin/sh\ntouch '"+marker+"'\nexec /usr/bin/git \"$@\"\n")
			if err := os.Chmod(r.Git, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{own, bare, filepath.Join(bare, "refs")} {
				log.Reset()
				_, err := r.Resolve(t.Context(), dir)
				requireRule(t, err, RuleOwnDirectory, log)
			}
			if _, err := os.Lstat(marker); !os.IsNotExist(err) {
				t.Errorf("git ran for a directory in Wraith Box's own %s: %v", field, err)
			}
		})
	}
}
