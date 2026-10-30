//go:build darwin || linux

package shell

import (
	"cmp"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	guestv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/guest/v1"
	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/guestd/users"
)

// TestRefusals: each refusal of decision 1 of #303 comes back with its
// rule, which the log has too, and starts nothing.
func TestRefusals(t *testing.T) {
	env := func(vs ...string) *terminalv1.Setup {
		s := &terminalv1.Setup{}
		for i := 0; i < len(vs); i += 2 {
			s.Env = append(s.Env, &terminalv1.EnvVar{Name: vs[i], Value: vs[i+1]})
		}
		return s
	}
	tests := []struct {
		name    string
		id      string
		user    string
		setup   *terminalv1.Setup
		change  func(h *harness)
		rule    string
		invalid bool
	}{
		{name: "shell id form", id: "proj-2026-1200-ab12", rule: RuleStartInvalid, invalid: true},
		{name: "shell id dots", id: "a..b-20261030-1200-ab12", rule: RuleStartInvalid, invalid: true},
		{name: "shell id length", id: strings.Repeat("a", 120) + "-20261030-1200-ab12", rule: RuleStartInvalid, invalid: true},
		{name: "no setup", setup: nil, rule: RuleStartInvalid, invalid: true},
		{name: "size 0", setup: interactive(0, 80), rule: RuleStartInvalid, invalid: true},
		{name: "size over 65535", setup: interactive(24, 65536), rule: RuleStartInvalid, invalid: true},
		{name: "no user", user: "-", rule: RuleStartInvalid, invalid: true},
		{name: "env name", setup: env("PATH", "/tmp"), rule: RuleEnv, invalid: true},
		{name: "env twice", setup: env("TERM", "a", "TERM", "b"), rule: RuleEnv, invalid: true},
		{name: "env value byte", setup: env("TERM", "a\nb"), rule: RuleEnv, invalid: true},
		{name: "env value length", setup: env("LANG", strings.Repeat("a", 257)), rule: RuleEnv, invalid: true},
		{name: "mount", change: func(h *harness) {
			h.srv.cfg.MountCheck = func(context.Context) string { return "data-mount-flags" }
		}, rule: "data-mount-flags"},
		{name: "unknown user", user: "wb-other", rule: RuleUserUnknown},
		{name: "root", change: func(h *harness) { h.users.u.UID = 0 }, rule: RuleUserPrivileged},
		{name: "admin", change: func(h *harness) { h.users.privileged = true }, rule: RuleUserPrivileged},
		{name: "directory", change: func(h *harness) { h.users.dirErr = errors.New("dscl") }, rule: users.RuleDirectory},
		{name: "locked", change: func(h *harness) { h.users.holdErr = users.ErrLocked }, rule: RuleUserLocked},
		{name: "changed", change: func(h *harness) { h.users.holdErr = users.ErrUnknown }, rule: RuleUserUnknown},
		{name: "id in use", change: func(h *harness) {
			h.srv.open[testShellID] = &session{}
		}, rule: RuleIDInUse},
		{name: "helper start", change: func(h *harness) {
			h.srv.cfg.Helper = []string{"/nonexistent/wb-guestd", HelperArg}
		}, rule: RuleStartFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "exit:0")
			if tt.change != nil {
				tt.change(h)
			}
			id := cmp.Or(tt.id, testShellID)
			user := cmp.Or(tt.user, h.users.u.Name)
			if user == "-" {
				user = ""
			}
			setup := tt.setup
			if setup == nil && tt.name != "no setup" {
				setup = &terminalv1.Setup{}
			}
			err := h.srv.Shell(startStream(t, id, user, setup))
			wantRefused(t, h, err, tt.rule, tt.invalid)
		})
	}
}

// wantRefused checks that err is a refusal with rule, that the log has
// the rule, and that no shell started.
func wantRefused(t *testing.T, h *harness, err error, rule string, invalid bool) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Rule != rule || r.Invalid != invalid {
		t.Fatalf("Shell = %v, want rule %s, invalid %v", err, rule, invalid)
	}
	if !strings.Contains(h.log.String(), `"rule":"`+rule+`"`) {
		t.Errorf("log has no rule %s", rule)
	}
	if strings.Contains(h.log.String(), "shell started") {
		t.Error("a refused shell started")
	}
}

// TestRefuseFrameFirst: a first request that isn't a start is refused.
func TestRefuseFrameFirst(t *testing.T) {
	h := newHarness(t, "exit:0")
	st := startStream(t, "", "", nil)
	st.in = make(chan *guestv1.ShellRequest, 1)
	st.in <- &guestv1.ShellRequest{Msg: &guestv1.ShellRequest_Frame{Frame: &terminalv1.ClientFrame{}}}
	var r *Refusal
	if err := h.srv.Shell(st); !errors.As(err, &r) || r.Rule != RuleStartInvalid || !r.Invalid {
		t.Fatalf("Shell = %v", err)
	}
}

func startStream(t *testing.T, id, user string, setup *terminalv1.Setup) *fakeStream {
	st := &fakeStream{ctx: t.Context(), in: make(chan *guestv1.ShellRequest, 1), out: make(chan *terminalv1.ServerFrame, 16)}
	st.in <- &guestv1.ShellRequest{Msg: &guestv1.ShellRequest_Start{Start: &guestv1.ShellStart{
		ShellId: id, User: user, Setup: setup,
	}}}
	return st
}

// TestBadReports: a report out of form or after the exit ends the shell
// with session-helper-report, and kills the session.
func TestBadReports(t *testing.T) {
	for _, mode := range []string{"unknown-kind", "after-exit", "code-256"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t, "")
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			h.srv.cfg.Helper = []string{exe, testRawHelperArg, mode}
			h.start(&terminalv1.Setup{})
			last := h.end()
			if last.GetFailure().GetRule() != RuleHelperReport {
				t.Fatalf("last frame %v, want a failure %s", last, RuleHelperReport)
			}
			if h.count(func(f *terminalv1.ServerFrame) bool { return f.GetExit() != nil }) != 0 {
				t.Error("an exit frame for a bad report")
			}
			if !strings.Contains(h.log.String(), `"decision":"kill"`) {
				t.Error("the helper wasn't killed")
			}
		})
	}
}

// TestStreamEnd: a stream that ends on a live connection hangs up, and
// one that ends with the link leaves the shell to the stop loop.
func TestStreamEnd(t *testing.T) {
	t.Run("live connection", func(t *testing.T) {
		h := newHarness(t, "sleep")
		h.start(&terminalv1.Setup{})
		h.waitOutput("ready")
		h.cancel()
		h.end()
		if !strings.Contains(h.log.String(), `"reason":"stream-ended"`) {
			t.Error("no hangup")
		}
	})
	t.Run("link drop", func(t *testing.T) {
		h := newHarness(t, "sleep")
		h.srv.cfg.Dropped = func(context.Context) bool { return true }
		h.start(&terminalv1.Setup{})
		h.waitOutput("ready")
		sid := h.sid()
		h.cancel()
		deadline := time.Now().Add(10 * time.Second)
		for !strings.Contains(h.log.String(), "leave-to-stop-loop") && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if strings.Contains(h.log.String(), `"decision":"hangup"`) {
			t.Error("hung up after a link drop")
		}
		// The stop loop and the user's kill loop end such a shell. Here
		// the test kills it.
		h.srv.mu.Lock()
		sn := h.srv.open[testShellID]
		h.srv.mu.Unlock()
		sn.kill()
		if err := <-h.done; err != nil {
			t.Fatal(err)
		}
		h.srv.mu.Lock()
		_, open := h.srv.open[testShellID]
		h.srv.mu.Unlock()
		if open {
			t.Errorf("shell %d still open", sid)
		}
	})
}

// TestInvalidFrame: a frame out of form is logged with its rule and
// dropped, and the shell runs on.
func TestInvalidFrame(t *testing.T) {
	h := newHarness(t, "cat")
	h.start(&terminalv1.Setup{})
	h.send(&terminalv1.ClientFrame{})
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Signal{Signal: terminalv1.Signal_SIGNAL_UNSPECIFIED}})
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Resize{Resize: &terminalv1.WindowSize{Rows: 1, Cols: 1}}})
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_StdinClosed{StdinClosed: &terminalv1.StdinClosed{}}})
	wantExitCode(t, h.end(), 0)
	if n := strings.Count(h.log.String(), RuleFrameInvalid); n != 3 {
		t.Errorf("%d frames dropped, want 3", n)
	}
}
