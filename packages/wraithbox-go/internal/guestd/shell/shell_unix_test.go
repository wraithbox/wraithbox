//go:build darwin || linux

package shell

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	terminalv1 "github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/gen/wraithbox/terminal/v1"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/sessionproc"
)

const stdout, stderr = terminalv1.Stream_STREAM_STDOUT, terminalv1.Stream_STREAM_STDERR

func isStopped(f *terminalv1.ServerFrame) bool { return f.GetStopped() != nil }

// TestSessionLeader: the helper leads the POSIX session, the program
// leads its own process group, which is the PTY's foreground group, and
// the PTY is their controlling terminal.
func TestSessionLeader(t *testing.T) {
	h := newHarness(t, "info")
	h.start(interactive(24, 80))
	h.waitOutput(" end")
	wantExitCode(t, h.end(), 0)
	var pid, ppid, sid, pgid, fg int
	var ctty bool
	_, out, found := strings.Cut(h.output(stdout), "pid=")
	if !found {
		t.Fatalf("output %q has no pid", h.output(stdout))
	}
	if _, err := fmt.Sscanf(out, "%d ppid=%d sid=%d pgid=%d fg=%d ctty=%t",
		&pid, &ppid, &sid, &pgid, &fg, &ctty); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	if sid != ppid || pgid != pid || fg != pid || !ctty {
		t.Errorf("pid %d, helper %d, session %d, group %d, foreground %d, controlling terminal %v",
			pid, ppid, sid, pgid, fg, ctty)
	}
}

// TestStartHoldsStops: a shell start waits for a running stop loop, and
// keeps a new one from starting until its helper's POSIX session is
// known (S13-01-process-hardening, "When the link drops").
func TestStartHoldsStops(t *testing.T) {
	h := newHarness(t, "sleep")
	h.stop.gate = make(chan struct{})
	h.start(interactive(24, 80))
	select {
	case f := <-h.st.out:
		t.Fatalf("frame %v while a stop loop ran", f)
	case <-time.After(200 * time.Millisecond):
	}
	if sids := h.srv.openSessions(); len(sids) != 0 {
		t.Fatalf("a helper started while a stop loop ran: %v", sids)
	}
	close(h.stop.gate)
	h.waitOutput("ready")
	h.stop.mu.Lock()
	holds, released := h.stop.holds, slices.Clone(h.stop.released)
	h.stop.mu.Unlock()
	if holds != 1 || !slices.Equal(released, []int{1}) {
		t.Errorf("holds %d, open sessions at each release %v; want 1 and [1]", holds, released)
	}
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Hangup{Hangup: &terminalv1.Hangup{}}})
	h.end()
}

// TestCtrlCAndResize: a Ctrl-C byte reaches the program as SIGINT, and a
// resize as SIGWINCH with the new size.
func TestCtrlCAndResize(t *testing.T) {
	h := newHarness(t, "signals")
	h.start(interactive(24, 80))
	h.waitOutput("ready")
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Resize{Resize: &terminalv1.WindowSize{Rows: 40, Cols: 120}}})
	h.waitOutput("got SIGWINCH 40x120")
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Input{Input: []byte{0x03}}})
	h.waitOutput("got SIGINT")
	wantExitCode(t, h.end(), 0)
}

// TestStopAndResume: a program that stops itself gives one stopped
// frame, and resume continues it.
func TestStopAndResume(t *testing.T) {
	h := newHarness(t, "stop")
	h.start(&terminalv1.Setup{})
	h.waitFor("a stopped frame", func() bool { return h.count(isStopped) > 0 })
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Resume{Resume: &terminalv1.Resume{}}})
	h.waitOutput("continued")
	wantExitCode(t, h.end(), 0)
	if n := h.count(isStopped); n != 1 {
		t.Errorf("%d stopped frames, want 1", n)
	}
}

// TestExit: exit codes 0 and 3 and death by SIGKILL are reported, after
// the output written before the exit.
func TestExit(t *testing.T) {
	for _, tt := range []struct {
		mode, out string
		code      uint32
		signal    uint32
	}{
		{"exit:0", "out before exit", 0, 0},
		{"exit:3", "out before exit", 3, 0},
		{"kill-self", "bye", 0, 9},
	} {
		for _, setup := range []*terminalv1.Setup{{}, interactive(24, 80)} {
			t.Run(fmt.Sprintf("%s interactive=%v", tt.mode, setup.GetInteractive()), func(t *testing.T) {
				h := newHarness(t, tt.mode)
				h.start(setup)
				last := h.end()
				if !strings.Contains(h.output(stdout), tt.out) {
					t.Errorf("output %q, want %q before the exit", h.output(stdout), tt.out)
				}
				if tt.signal != 0 {
					wantExitSignal(t, last, tt.signal)
					return
				}
				wantExitCode(t, last, tt.code)
			})
		}
	}
}

// TestInteractiveHangup: an interactive hangup gives the program SIGHUP.
func TestInteractiveHangup(t *testing.T) {
	h := newHarness(t, "sleep")
	h.start(interactive(24, 80))
	h.waitOutput("ready")
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Hangup{Hangup: &terminalv1.Hangup{}}})
	wantExitSignal(t, h.end(), uint32(syscall.SIGHUP))
	if strings.Contains(h.log.String(), RuleKilled) {
		t.Error("killed a program that ended on the hangup")
	}
}

// TestHangupKill: a program that ignores the hangup is killed after the
// delay, and so is nothing outside the shell's POSIX session or of
// another user.
func TestHangupKill(t *testing.T) {
	h := newHarness(t, "ignore-hup")
	table := &otherUserTable{inner: sessionproc.System{}, srv: h.srv, uid: h.users.u.UID}
	h.srv.cfg.Table = table
	h.start(interactive(24, 80))
	h.waitOutput("ready")
	h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Hangup{Hangup: &terminalv1.Hangup{}}})
	wantExitSignal(t, h.end(), 9)
	killed := table.killedPIDs()
	if len(killed) == 0 {
		t.Fatal("killed nothing")
	}
	if slices.Contains(killed, otherPID) || slices.Contains(killed, mixedPID) {
		t.Errorf("killed another user's process: %v", killed)
	}
	log := h.log.String()
	for _, want := range []string{`"decision":"leave-other-user"`, `"rule":"` + RuleKilled + `"`} {
		if !strings.Contains(log, want) {
			t.Errorf("log has no %s", want)
		}
	}
	if len(h.stop.forgot) == 0 {
		t.Error("the hangup forgot no stopped process")
	}
}

// TestNonInteractive: standard input EOF reaches the program, standard
// error has its own frames, and a closed standard output gives EPIPE.
func TestNonInteractive(t *testing.T) {
	t.Run("stdin eof", func(t *testing.T) {
		h := newHarness(t, "cat")
		h.start(&terminalv1.Setup{})
		h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_Input{Input: []byte("hello\n")}})
		h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_StdinClosed{StdinClosed: &terminalv1.StdinClosed{}}})
		wantExitCode(t, h.end(), 0)
		if h.output(stdout) != "hello\n" || h.output(stderr) != "stdin eof\n" {
			t.Errorf("stdout %q, stderr %q", h.output(stdout), h.output(stderr))
		}
	})
	t.Run("stderr", func(t *testing.T) {
		h := newHarness(t, "stderr")
		h.start(&terminalv1.Setup{})
		wantExitCode(t, h.end(), 0)
		if h.output(stdout) != "to-out" || h.output(stderr) != "to-err" {
			t.Errorf("stdout %q, stderr %q", h.output(stdout), h.output(stderr))
		}
	})
	t.Run("interactive separate stderr", func(t *testing.T) {
		h := newHarness(t, "stderr")
		setup := interactive(24, 80)
		setup.SeparateStderr = true
		h.start(setup)
		wantExitCode(t, h.end(), 0)
		if !strings.Contains(h.output(stdout), "to-out") || h.output(stderr) != "to-err" {
			t.Errorf("stdout %q, stderr %q", h.output(stdout), h.output(stderr))
		}
	})
	t.Run("epipe", func(t *testing.T) {
		h := newHarness(t, "epipe")
		h.start(&terminalv1.Setup{})
		h.waitOutput("line")
		h.send(&terminalv1.ClientFrame{Frame: &terminalv1.ClientFrame_StdoutClosed{StdoutClosed: &terminalv1.StdoutClosed{}}})
		wantExitCode(t, h.end(), 0)
		if h.output(stderr) != "EPIPE" {
			t.Errorf("stderr %q, want EPIPE", h.output(stderr))
		}
	})
}

// TestHelperGone: a killed helper gives session-helper-gone, and the
// kill of what is left of its session.
func TestHelperGone(t *testing.T) {
	h := newHarness(t, "ignore-hup")
	h.start(&terminalv1.Setup{})
	h.waitOutput("ready")
	if err := syscall.Kill(int(h.sid()), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	last := h.end()
	if last.GetFailure().GetRule() != RuleHelperGone {
		t.Fatalf("last frame %v, want a failure %s", last, RuleHelperGone)
	}
	if !strings.Contains(h.log.String(), `"decision":"kill"`) {
		t.Error("nothing killed")
	}
}

// TestEnvironment: the program's environment is exactly that of
// decision 2 of #303, and it starts in the home directory.
func TestEnvironment(t *testing.T) {
	h := newHarness(t, "env")
	h.start(&terminalv1.Setup{Env: []*terminalv1.EnvVar{
		{Name: "TERM", Value: "xterm-256color"}, {Name: "LANG", Value: "en_US.UTF-8"},
	}})
	wantExitCode(t, h.end(), 0)
	u := h.users.u
	want := []string{
		"HOME=" + u.Home, "LANG=en_US.UTF-8", "LOGNAME=" + u.Name, "PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"SHELL=/bin/zsh", "TERM=xterm-256color", "USER=" + u.Name,
	}
	tmp, err := sessionproc.UserTempDir(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if tmp != "" {
		want = append(want, "TMPDIR="+tmp)
	}
	slices.Sort(want)
	lines := strings.Split(strings.TrimSpace(h.output(stdout)), "\n")
	cwd := lines[len(lines)-1]
	if got := lines[:len(lines)-1]; !slices.Equal(got, want) {
		t.Errorf("environment\n%q\nwant\n%q", got, want)
	}
	home, _ := filepath.EvalSymlinks(u.Home)
	wd, _ := filepath.EvalSymlinks(strings.TrimPrefix(cwd, "cwd="))
	if wd != home {
		t.Errorf("working directory %q, want %q", wd, home)
	}
}

// TestReplicaOwner: the PTY's replica, the program's standard input,
// belongs to the user with mode 0600.
func TestReplicaOwner(t *testing.T) {
	h := newHarness(t, "tty-stat")
	h.start(interactive(24, 80))
	wantExitCode(t, h.end(), 0)
	want := fmt.Sprintf("owner=%d mode=600", h.users.u.UID)
	if !strings.Contains(h.output(stdout), want) {
		t.Errorf("output %q, want %q", h.output(stdout), want)
	}
}
