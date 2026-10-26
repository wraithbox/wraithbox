//go:build unix

package setup

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/service"
)

// TestC12SetupRefusals feeds wb setup each case of C12-install-and-downloads,
// "wb setup", and sees each refused with its rule and nothing written:
// no file, no link, no directory, and no change to the agent.
func TestC12SetupRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fixture)
		rule  Rule
		says  []string
	}{
		{"bundle outside both Applications folders", func(f *fixture) {
			moved := filepath.Join(f.root, "build", appbundle.AppName)
			f.mkdir(filepath.Dir(moved))
			if err := os.Rename(f.bundle, moved); err != nil {
				f.t.Fatal(err)
			}
			f.env.Executable = filepath.Join(moved, "Contents", "MacOS", "wb")
		}, RuleBundleLocation, []string{"not directly in ~/Applications or /Applications"}},
		{"bundle in a folder inside Applications", func(f *fixture) {
			moved := filepath.Join(f.home, "Applications", "old", appbundle.AppName)
			f.mkdir(filepath.Dir(moved))
			if err := os.Rename(f.bundle, moved); err != nil {
				f.t.Fatal(err)
			}
			f.env.Executable = filepath.Join(moved, "Contents", "MacOS", "wb")
		}, RuleBundleLocation, nil},
		{"bare wb", func(f *fixture) {
			f.env.Executable = filepath.Join(f.root, "bin", "wb")
			f.write(f.env.Executable, "wb")
		}, RuleNotInBundle, []string{"not Contents/MacOS/wb of a bundle"}},
		{"unstamped wb", func(f *fixture) { f.env.Version = "" }, RuleNotInBundle, []string{"unstamped"}},
		{"other WBBuildVersion", func(f *fixture) {
			f.write(filepath.Join(f.bundle, "Contents", "Info.plist"), infoPlist("0.2.0"))
		}, RuleNotInBundle, []string{`WBBuildVersion "0.2.0"`, `build "` + testVersion + `"`}},
		{"binary Info.plist", func(f *fixture) {
			f.write(filepath.Join(f.bundle, "Contents", "Info.plist"), "bplist00")
		}, RuleNotInBundle, []string{"binary"}},
		{"no wb-hostd", func(f *fixture) {
			if err := os.Remove(filepath.Join(f.bundle, "Contents", "MacOS", "wb-hostd")); err != nil {
				f.t.Fatal(err)
			}
		}, RuleNotInBundle, []string{"no program Contents/MacOS/wb-hostd"}},
		{"regular file at ~/.local/bin/wb", func(f *fixture) {
			f.write(filepath.Join(f.home, ".local", "bin", "wb"), "#!/bin/sh\n")
		}, RuleLinkExists, []string{"not a symbolic link"}},
		{"link into another directory", func(f *fixture) {
			f.symlink("/usr/local/bin/wb", filepath.Join(f.home, ".local", "bin", "wb"))
		}, RuleLinkExists, []string{`"/usr/local/bin/wb", which is not Contents/MacOS/wb`}},
		{"link into a bundle without WBBuildVersion", func(f *fixture) {
			other := filepath.Join(f.root, "Other.app")
			f.write(filepath.Join(other, "Contents", "MacOS", "wb"), "wb")
			f.write(filepath.Join(other, "Contents", "Info.plist"), strings.Replace(infoPlist("1.0.0"), "WBBuildVersion", "Other", 1))
			f.symlink(filepath.Join(other, "Contents", "MacOS", "wb"), filepath.Join(f.home, ".local", "bin", "wb"))
		}, RuleLinkExists, []string{"has no WBBuildVersion"}},
		{"changed plist with a VM running", func(f *fixture) {
			f.hostd.st = HostdState{Kind: HostdSameVersion, Listed: true, VMs: []VM{{Name: "macos-work", Sessions: []string{"s1", "s\x1b2"}}}}
		}, RuleVMsRunning, []string{`"macos-work" (sessions "s1", "s\x1b2")`, "wb vm stop"}},
		{"same-version wb-hostd that can't list VMs", func(f *fixture) {
			f.hostd.st = HostdState{Kind: HostdSameVersion}
		}, RuleVMsRunning, []string{"can't list its VMs", "launchctl bootout gui/501/nl.wraithbox.hostd"}},
		{"wb-hostd of another build version", func(f *fixture) {
			f.hostd.st = HostdState{Kind: HostdOtherVersion, Peer: "0.0.9"}
		}, RuleVMsRunning, []string{"build 0.0.9 runs", `this wb is build "` + testVersion + `"`, "with the wb of build 0.0.9"}},
		{"wb-hostd that can't be asked", func(f *fixture) {
			f.hostd.err = errors.New("ipc-peer-uid")
		}, RuleVMsRunning, []string{"can't ask wb-hostd", "ipc-peer-uid"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			f.wantRefused(tc.rule, f.tree())
			f.wantStderr(tc.says...)
		})
	}
}

// TestC12SetupRealPaths checks the real-path part of the bundle check
// (S17-02-setup, "The bundle check"): wb run through a link to the
// bundle's wb goes on, and a bundle that ~/Applications holds only as a
// link to a directory elsewhere is refused with setup-bundle-location,
// nothing written.
func TestC12SetupRealPaths(t *testing.T) {
	f := newFixture(t)
	f.env.Executable = filepath.Join(f.root, "bin", "wb")
	f.symlink(filepath.Join(f.bundle, "Contents", "MacOS", "wb"), f.env.Executable)
	if code := f.run(); code != ExitOK {
		t.Fatalf("wb through a link: exit %d, stderr %q", code, f.stderr.String())
	}
	f.wantLink()

	f = newFixture(t)
	elsewhere := filepath.Join(f.root, "build", appbundle.AppName)
	f.mkdir(filepath.Dir(elsewhere))
	if err := os.Rename(f.bundle, elsewhere); err != nil {
		t.Fatal(err)
	}
	f.symlink(elsewhere, f.bundle)
	f.wantRefused(RuleBundleLocation, f.tree())
	f.wantStderr(elsewhere)
}

func (f *fixture) symlink(target, path string) {
	f.t.Helper()
	f.mkdir(filepath.Dir(path))
	if err := os.Symlink(target, path); err != nil {
		f.t.Fatal(err)
	}
}

// TestC12SetupUnchangedPlist leaves a loaded agent with the same plist
// bytes running: no change to the agent, and no wb-hostd query.
func TestC12SetupUnchangedPlist(t *testing.T) {
	f := newFixture(t)
	f.svc.state = service.State{Current: true, Loaded: true}
	f.hostd.err = errors.New("asked")
	if code := f.run(); code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, f.stderr.String())
	}
	if f.svc.changed() || f.hostd.asked != 0 {
		t.Errorf("service calls %q, wb-hostd asked %d times; want neither", f.svc.calls, f.hostd.asked)
	}

	f = newFixture(t)
	f.svc.state = service.State{Current: true}
	if code := f.run(); code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, f.stderr.String())
	}
	if want := []string{"state " + f.hostdProgram(), "start"}; !slices.Equal(f.svc.calls, want) || f.hostd.asked != 0 {
		t.Errorf("service calls %q, wb-hostd asked %d times; want %q and none", f.svc.calls, f.hostd.asked, want)
	}
}

func (f *fixture) hostdProgram() string {
	return filepath.Join(f.bundle, "Contents", "MacOS", "wb-hostd")
}

// TestC12SetupGoesOn installs the agent and the link when no wb-hostd
// listens, and when wb-hostd lists no running VM.
func TestC12SetupGoesOn(t *testing.T) {
	for name, st := range map[string]HostdState{
		"no wb-hostd":         {Kind: HostdNotListening},
		"wb-hostd with no VM": {Kind: HostdSameVersion, Listed: true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.hostd.st = st
			if code := f.run(); code != ExitOK {
				t.Fatalf("exit %d, stderr %q", code, f.stderr.String())
			}
			if want := []string{"state " + f.hostdProgram(), "replace " + f.hostdProgram()}; !slices.Equal(f.svc.calls, want) {
				t.Errorf("service calls %q, want %q", f.svc.calls, want)
			}
			if f.hostd.asked != 1 {
				t.Errorf("wb-hostd asked %d times, want 1", f.hostd.asked)
			}
			f.wantLink()
		})
	}
}

func (f *fixture) wantLink() {
	f.t.Helper()
	to, err := os.Readlink(filepath.Join(f.home, ".local", "bin", "wb"))
	if want := filepath.Join(f.bundle, "Contents", "MacOS", "wb"); err != nil || to != want {
		f.t.Errorf("~/.local/bin/wb links to %q (%v), want %q", to, err, want)
	}
	entries, err := os.ReadDir(filepath.Join(f.home, ".local", "bin"))
	if err != nil || len(entries) != 1 {
		f.t.Errorf("~/.local/bin holds %v (%v), want only wb", entries, err)
	}
}

// TestC12SetupReplacesOldLink replaces a link into another Wraith Box
// bundle, a relative one included, and keeps one that is already right.
func TestC12SetupReplacesOldLink(t *testing.T) {
	for name, target := range map[string]string{
		"absolute": "", // set below, to the old bundle's wb
		"relative": "../../old/Wraith Box.app/Contents/MacOS/wb",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			old := filepath.Join(f.home, "old", appbundle.AppName)
			f.writeBundle(old, "0.0.1")
			if target == "" {
				target = filepath.Join(old, "Contents", "MacOS", "wb")
			}
			f.symlink(target, filepath.Join(f.home, ".local", "bin", "wb"))
			if code := f.run(); code != ExitOK {
				t.Fatalf("exit %d, stderr %q", code, f.stderr.String())
			}
			f.wantLink()
		})
	}

	f := newFixture(t)
	f.symlink(filepath.Join(f.bundle, "Contents", "MacOS", "wb"), filepath.Join(f.home, ".local", "bin", "wb"))
	if code := f.run(); code != ExitOK || !strings.Contains(f.stdout.String(), "already links to") {
		t.Fatalf("exit %d, stdout %q; want the link kept", code, f.stdout.String())
	}
	f.wantLink()
}

// TestC12SetupPATH prints the line to add when ~/.local/bin isn't on
// PATH, and makes ~/.local/bin when it is missing.
func TestC12SetupPATH(t *testing.T) {
	f := newFixture(t)
	f.env.PATH = "/usr/bin:/bin"
	if code := f.run(); code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, f.stderr.String())
	}
	if !strings.Contains(f.stdout.String(), `export PATH="$HOME/.local/bin:$PATH"`) {
		t.Errorf("stdout = %q, want the line to add", f.stdout.String())
	}
	f.wantLink()

	f = newFixture(t)
	f.env.PATH = "/usr/bin:" + filepath.Join(f.home, ".local", "bin") + "/"
	if code := f.run(); code != ExitOK || strings.Contains(f.stdout.String(), "export PATH") {
		t.Errorf("exit %d, stdout %q; want no line with ~/.local/bin on PATH", code, f.stdout.String())
	}
}

// TestC12SetupProjectsRoot makes the projects root and the probe file
// when they are missing, mode 0700 and 0600, and leaves them as they are
// when they are there (S04-06-git-confinement, "The profile probes").
func TestC12SetupProjectsRoot(t *testing.T) {
	f := newFixture(t)
	if code := f.run(); code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, f.stderr.String())
	}
	probe := ProbeFile(f.env.Paths.Data)
	for path, mode := range map[string]os.FileMode{
		f.env.Paths.Config:                0o700,
		f.env.Paths.Logs:                  0o700,
		ProjectsRoot(f.env.Paths.Data):    0o700,
		filepath.Dir(filepath.Dir(probe)): 0o700,
		filepath.Dir(probe):               0o700,
		probe:                             0o600,
	} {
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode().Perm() != mode {
			t.Errorf("%s: %v %v, want mode %04o", path, fi, err, mode)
		}
	}

	f = newFixture(t)
	probe = ProbeFile(f.env.Paths.Data)
	f.mkdir(filepath.Dir(probe))
	root := ProjectsRoot(f.env.Paths.Data)
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatal(err)
	}
	f.write(probe, "kept")
	if code := f.run(); code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, f.stderr.String())
	}
	if data, _ := os.ReadFile(probe); string(data) != "kept" {
		t.Errorf("probe file holds %q, want it left as it was", data)
	}
	if fi, _ := os.Lstat(root); fi.Mode().Perm() != 0o750 {
		t.Errorf("projects root mode %04o, want it left at 0750", fi.Mode().Perm())
	}
}

// TestSetupServiceErrors reports a failed state read before anything is
// written.
func TestSetupServiceErrors(t *testing.T) {
	f := newFixture(t)
	f.svc.stateErr = errors.New("launchctl print exited with 5")
	before := f.tree()
	if code := f.run(); code != ExitRefused || !strings.Contains(f.stderr.String(), "exited with 5") {
		t.Errorf("exit %d, stderr %q; want the error", code, f.stderr.String())
	}
	if !slices.Equal(before, f.tree()) {
		t.Error("a failed state read changed the files")
	}
}
