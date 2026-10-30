package setup

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/appbundle"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/codesign"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/paths"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/service"
)

const testVersion = "0.1.0-dev+g1234abc"

// fakeService is a service.Manager that records each call.
type fakeService struct {
	state    service.State
	stateErr error
	calls    []string
}

func (f *fakeService) State(_ context.Context, program string) (service.State, error) {
	f.calls = append(f.calls, "state "+program)
	return f.state, f.stateErr
}

func (f *fakeService) Replace(_ context.Context, program string) error {
	f.calls = append(f.calls, "replace "+program)
	return nil
}

func (f *fakeService) Start(context.Context) error {
	f.calls = append(f.calls, "start")
	return nil
}

// changed reports whether a call changed the agent.
func (f *fakeService) changed() bool {
	return slices.ContainsFunc(f.calls, func(c string) bool { return !strings.HasPrefix(c, "state ") })
}

// fakeHostd is a Hostd with a fixed answer that counts the questions.
type fakeHostd struct {
	st    HostdState
	err   error
	asked int
}

func (f *fakeHostd) State(context.Context) (HostdState, error) {
	f.asked++
	return f.st, f.err
}

// The team of the fixture's bundle, and the name its profiles give it.
const (
	testTeam     = "ABCDE12345"
	testTeamName = "Test & Team"
)

// testProfile is a decoded profile of team, as `security cms -D -i`
// prints it, cut short.
func testProfile(team, name string) string {
	var esc strings.Builder
	if err := xml.EscapeText(&esc, []byte(name)); err != nil {
		panic(err)
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>ExpirationDate</key>
	<date>2027-10-20T09:00:00Z</date>
	<key>TeamIdentifier</key>
	<array>
		<string>` + team + `</string>
	</array>
	<key>TeamName</key>
	<string>` + esc.String() + `</string>
</dict>
</plist>
`
}

// readTeam is the fixture's ReadTeam: the team in f.teams of the helper
// bundle at path. A helper not in f.teams has an unreadable signature.
func (f *fixture) readTeam(_ context.Context, path string) (string, error) {
	name := strings.TrimSuffix(filepath.Base(path), ".app")
	team, ok := f.teams[name]
	if !ok {
		return "", fmt.Errorf("%s: code object is not signed at all", path)
	}
	return team, nil
}

func (f *fixture) profilePath(helper string) string {
	return filepath.Join(f.bundle, filepath.FromSlash(appbundle.HelperDir(helper)), "Contents", appbundle.ProfileName)
}

// fixture is an Env in a temporary directory: a home with
// ~/Applications/Wraith Box.app of build testVersion, whose helper
// bundles testTeam signed and have its profile, an empty /Applications,
// a fake service manager with no agent, and no wb-hostd.
type fixture struct {
	t *testing.T
	// teams is the team of each helper bundle's signature, by helper.
	teams  map[string]string
	root   string
	home   string
	bundle string
	env    Env
	svc    *fakeService
	hostd  *fakeHostd
	stdout bytes.Buffer
	stderr bytes.Buffer
	log    bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		t:     t,
		root:  root,
		home:  filepath.Join(root, "home"),
		svc:   &fakeService{},
		hostd: &fakeHostd{},
	}
	f.bundle = filepath.Join(f.home, "Applications", appbundle.AppName)
	f.writeBundle(f.bundle, testVersion)
	f.teams = map[string]string{}
	for _, p := range appbundle.Programs {
		if p.Place == appbundle.PlaceHelper {
			f.teams[p.Name] = testTeam
			f.write(f.profilePath(p.Name), testProfile(testTeam, testTeamName))
		}
	}
	sys := filepath.Join(root, "Applications")
	f.mkdir(sys)
	data := filepath.Join(f.home, "Library", "Application Support", "WraithBox")
	f.env = Env{
		Host:        platform.Host{OS: platform.MacOS},
		ReadRelease: func() (platform.Release, error) { return platform.Release{AppleSilicon: true, Version: "27.0"}, nil },
		Home:        f.home,
		UID:         501,
		Paths: paths.Paths{
			Config: filepath.Join(f.home, ".config", "wraithbox"),
			Data:   data,
			Logs:   filepath.Join(f.home, "Library", "Logs", "WraithBox"),
			Run:    filepath.Join(data, "run"),
		},
		PATH:               strings.Join([]string{filepath.Join(f.home, ".local", "bin"), "/usr/bin"}, string(filepath.ListSeparator)),
		Executable:         filepath.Join(f.bundle, "Contents", "MacOS", "wb"),
		Version:            testVersion,
		SystemApplications: sys,
		Service:            f.svc,
		Hostd:              f.hostd,
		CheckGit:           func(context.Context) error { return nil },
		ReadTeam:           f.readTeam,
		ReadProfile: func(_ context.Context, path string) (codesign.Profile, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return codesign.Profile{}, err
			}
			return codesign.ParseProfile(data)
		},
		Stdout: &f.stdout,
		Stderr: &f.stderr,
		Log:    slog.New(slog.NewTextHandler(&f.log, nil)),
	}
	return f
}

// writeBundle writes a bundle with wb, wb-hostd and an Info.plist of
// build v, and a helper bundle directory for each helper.
func (f *fixture) writeBundle(dir, v string) {
	f.t.Helper()
	f.write(filepath.Join(dir, "Contents", "MacOS", "wb"), "wb")
	f.write(filepath.Join(dir, "Contents", "MacOS", "wb-hostd"), "wb-hostd")
	f.write(filepath.Join(dir, "Contents", "Info.plist"), infoPlist(v))
	for _, p := range appbundle.Programs {
		if p.Place == appbundle.PlaceHelper {
			f.mkdir(filepath.Join(dir, filepath.FromSlash(appbundle.HelperDir(p.Name)), "Contents"))
		}
	}
}

func infoPlist(v string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>wb-notify</string>
	<key>WBBuildVersion</key>
	<string>` + v + `</string>
</dict>
</plist>
`
}

func (f *fixture) write(path, data string) {
	f.t.Helper()
	f.mkdir(filepath.Dir(path))
	if err := os.WriteFile(path, []byte(data), 0o700); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) mkdir(dir string) {
	f.t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		f.t.Fatal(err)
	}
}

// run runs wb setup and returns its exit status.
func (f *fixture) run(args ...string) int {
	f.t.Helper()
	return Run(context.Background(), f.env, args)
}

// tree lists every path under the fixture's root with its type and
// mode, and each link's target, to see that a refusal wrote nothing.
func (f *fixture) tree() []string {
	f.t.Helper()
	var out []string
	err := filepath.WalkDir(f.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		line := path + " " + fi.Mode().String()
		if d.Type()&fs.ModeSymlink != 0 {
			to, _ := os.Readlink(path)
			line += " -> " + to
		}
		out = append(out, line)
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// wantRefused checks the exit status, the rule on standard error, and
// that the refusal wrote nothing.
func (f *fixture) wantRefused(rule Rule, before []string) {
	f.t.Helper()
	if code := f.run(); code != ExitRefused {
		f.t.Errorf("exit %d, want %d", code, ExitRefused)
	}
	if !strings.Contains(f.stderr.String(), "refused ("+string(rule)+")") {
		f.t.Errorf("stderr = %q, want rule %s", f.stderr.String(), rule)
	}
	if !strings.Contains(f.log.String(), "rule="+string(rule)+" decision=refuse") {
		f.t.Errorf("log = %q, want the refusal with rule %s", f.log.String(), rule)
	}
	if f.svc.changed() {
		f.t.Errorf("the service manager got %q after a refusal", f.svc.calls)
	}
	if after := f.tree(); !slices.Equal(before, after) {
		f.t.Errorf("a refusal changed the files:\nbefore %q\nafter  %q", before, after)
	}
}

// wantStderr checks that standard error holds each of says.
func (f *fixture) wantStderr(says ...string) {
	f.t.Helper()
	for _, s := range says {
		if !strings.Contains(f.stderr.String(), s) {
			f.t.Errorf("stderr = %q, want %q", f.stderr.String(), s)
		}
	}
}
