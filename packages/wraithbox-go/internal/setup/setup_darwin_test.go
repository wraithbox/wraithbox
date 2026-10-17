package setup

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/service"
)

// launchctlRecorder answers launchctl from a code per subcommand, and
// records each run.
type launchctlRecorder struct {
	codes map[string]int
	calls []service.Command
}

func (r *launchctlRecorder) run(_ context.Context, c service.Command) (service.Result, error) {
	r.calls = append(r.calls, c)
	code, ok := r.codes[c.Args[0]]
	if !ok {
		return service.Result{}, errors.New("unexpected launchctl " + strings.Join(c.Args, " "))
	}
	return service.Result{Code: code}, nil
}

func (r *launchctlRecorder) args() []string {
	var out []string
	for _, c := range r.calls {
		if c.Path != service.LaunchctlPath || !slices.Equal(c.Env, []string{service.LaunchctlEnv}) {
			out = append(out, "BAD "+c.Path+" "+strings.Join(c.Env, ","))
		}
		out = append(out, strings.Join(c.Args, " "))
	}
	return out
}

// withLaunchAgent gives the fixture the real LaunchAgent manager in its
// temporary home, with launchctl recorded.
func (f *fixture) withLaunchAgent(codes map[string]int) *launchctlRecorder {
	f.t.Helper()
	rec := &launchctlRecorder{codes: codes}
	m, err := service.New(service.Config{Home: f.home, UID: f.env.UID, Run: rec.run, Log: f.env.Log})
	if err != nil {
		f.t.Fatal(err)
	}
	f.env.Service = m
	return rec
}

// TestC12SetupLaunchAgentUnchanged runs wb setup with the real
// LaunchAgent manager on a plist of the same bytes whose agent is
// loaded. launchctl runs only print, as /bin/launchctl with
// PATH=/usr/bin:/bin alone, and wb-hostd isn't asked.
func TestC12SetupLaunchAgentUnchanged(t *testing.T) {
	f := newFixture(t)
	rec := f.withLaunchAgent(map[string]int{"print": 0})
	data, err := service.Plist(f.hostdProgram())
	if err != nil {
		t.Fatal(err)
	}
	f.write(service.PlistPath(f.home), string(data))
	f.hostd.err = errors.New("asked")
	if code := f.run(); code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, f.stderr.String())
	}
	if want := []string{"print gui/501/nl.wraithbox.hostd"}; !slices.Equal(rec.args(), want) || f.hostd.asked != 0 {
		t.Errorf("launchctl runs %q, wb-hostd asked %d times; want %q and none", rec.args(), f.hostd.asked, want)
	}
}

// TestC12SetupLaunchAgentInstall installs the agent with the real
// LaunchAgent manager when there is none: the plist, mode 0644, then
// bootout, which counts as done for an agent that isn't loaded, and
// bootstrap.
func TestC12SetupLaunchAgentInstall(t *testing.T) {
	f := newFixture(t)
	rec := f.withLaunchAgent(map[string]int{"print": 113, "bootout": 113, "bootstrap": 0})
	if code := f.run(); code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, f.stderr.String())
	}
	plist := service.PlistPath(f.home)
	want := []string{
		"print gui/501/nl.wraithbox.hostd",
		"bootout gui/501/nl.wraithbox.hostd",
		"print gui/501/nl.wraithbox.hostd",
		"bootstrap gui/501 " + plist,
	}
	if !slices.Equal(rec.args(), want) {
		t.Errorf("launchctl runs %q, want %q", rec.args(), want)
	}
	fi, err := os.Stat(plist)
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("plist %v %v, want mode 0644", fi, err)
	}
	f.wantLink()
}
