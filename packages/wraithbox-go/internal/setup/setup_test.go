package setup

import (
	"errors"
	"strings"
	"testing"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform"
)

// TestC12SetupHostCheck refuses, through an injected host description,
// a Mac without Apple Silicon, an older macOS, one whose release can't
// be read, and Linux, WSL and Windows hosts, which are later in the
// matrix. Each refusal writes nothing (S12-03-host-prerequisites,
// "Prerequisites"; S12-01-platform-interfaces, "Unimplemented platforms
// fail clearly").
func TestC12SetupHostCheck(t *testing.T) {
	release := func(r platform.Release, err error) func() (platform.Release, error) {
		return func() (platform.Release, error) { return r, err }
	}
	tests := []struct {
		name    string
		host    platform.Host
		release func() (platform.Release, error)
		rule    Rule
		says    string
	}{
		{"Intel Mac", platform.Host{OS: platform.MacOS}, release(platform.Release{Version: "27.1"}, nil), RuleHostUnsupported, "no Apple Silicon"},
		{"macOS 14", platform.Host{OS: platform.MacOS}, release(platform.Release{AppleSilicon: true, Version: "14.7.1"}, nil), RuleHostUnsupported, `macOS "14.7.1"`},
		{"macOS 26", platform.Host{OS: platform.MacOS}, release(platform.Release{AppleSilicon: true, Version: "26.6"}, nil), RuleHostUnsupported, `macOS "26.6"`},
		{"unreadable version", platform.Host{OS: platform.MacOS}, release(platform.Release{AppleSilicon: true, Version: "x"}, nil), RuleHostUnsupported, `macOS "x"`},
		{"release error", platform.Host{OS: platform.MacOS}, release(platform.Release{}, errors.New("no sysctl")), RuleHostUnsupported, "no sysctl"},
		{"Linux", platform.Host{OS: platform.Linux}, nil, RuleHostLater, "Linux hosts is later"},
		{"WSL", platform.Host{OS: platform.Linux, WSL: true}, nil, RuleHostLater, "WSL hosts is later"},
		{"Windows", platform.Host{OS: platform.Windows}, nil, RuleHostLater, "Windows hosts is later"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.env.Host = tc.host
			f.env.ReadRelease = tc.release
			f.wantRefused(tc.rule, f.tree())
			f.wantStderr(tc.says, "macOS 27 or later")
		})
	}
}

func TestC12SetupHostCheckAccepts(t *testing.T) {
	for _, v := range []string{"27.0", "27", "27.0.1", "28.1"} {
		f := newFixture(t)
		f.env.ReadRelease = func() (platform.Release, error) { return platform.Release{AppleSilicon: true, Version: v}, nil }
		if err := checkHost(t.Context(), &f.env); err != nil {
			t.Errorf("macOS %s: %v", v, err)
		}
	}
}

// TestSetupUnavailableAfterTheHostCheck reports an Env that couldn't be
// built only after the host check, so a later host gets its own rule.
func TestSetupUnavailableAfterTheHostCheck(t *testing.T) {
	f := newFixture(t)
	f.env.Host = platform.Host{OS: platform.Windows}
	f.env.Unavailable = errors.ErrUnsupported
	f.wantRefused(RuleHostLater, f.tree())

	f = newFixture(t)
	f.env.Unavailable = errors.New("no user database")
	if code := f.run(); code != ExitRefused || !strings.Contains(f.stderr.String(), "no user database") {
		t.Errorf("exit %d, stderr %q; want the error", code, f.stderr.String())
	}
	if len(f.svc.calls) != 0 {
		t.Errorf("service calls %q, want none", f.svc.calls)
	}
}

func TestSetupArguments(t *testing.T) {
	for _, args := range [][]string{{"--remove"}, {"extra"}} {
		f := newFixture(t)
		before := f.tree()
		if code := f.run(args...); code != ExitUsage {
			t.Errorf("wb setup %q: exit %d, want %d", args, code, ExitUsage)
		}
		if len(f.svc.calls) != 0 || strings.Join(before, "\n") != strings.Join(f.tree(), "\n") {
			t.Errorf("wb setup %q changed something", args)
		}
	}
}
