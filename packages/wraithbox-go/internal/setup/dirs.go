package setup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ProjectsRoot is the projects root under <data> (S04-06-git-confinement).
func ProjectsRoot(data string) string { return filepath.Join(data, "projects") }

// ProbeFile is the probe file wb-hostd's profile probes write and link,
// <projects root>/.wb-probe/export.git/probe (S04-06-git-confinement,
// "The profile probes").
func ProbeFile(data string) string {
	return filepath.Join(ProjectsRoot(data), ".wb-probe", "export.git", "probe")
}

// makeDirs makes <config>, <data> and <logs>, the projects root, and
// the probe directory with its empty probe file, each only when it is
// missing (S05-03-management-commands, "`wb setup`", step 2;
// S04-06-git-confinement, "Who creates what"). wb-hostd's profile can't
// write the projects root, so wb setup makes it before it starts the
// service. New directories get mode 0700 and the probe file 0600: only
// the user's own processes read them. What exists is left as it is,
// mode included.
func makeDirs(env *Env, out io.Writer) error {
	probe := ProbeFile(env.Paths.Data)
	for _, dir := range []string{env.Paths.Config, env.Paths.Logs, filepath.Dir(probe)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("make %s: %w", dir, err)
		}
	}
	f, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("make the probe file %s: %w", probe, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close the probe file %s: %w", probe, err)
	}
	fmt.Fprintf(out, "wb setup: made the probe file %s\n", probe)
	return nil
}
